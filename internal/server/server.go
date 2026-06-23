package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/catalog"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/config"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/gnocchi"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/keystone"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/prom"
)

type contextKey string

const authContextKey contextKey = "auth-context"

type Server struct {
	cfg     *config.Config
	logger  *slog.Logger
	auth    *keystone.Client
	prom    *prom.Client
	catalog *catalog.Manager
	handler http.Handler
}

type AggregateRequest struct {
	Operations   any             `json:"operations"`
	ResourceType string          `json:"resource_type"`
	Search       json.RawMessage `json:"search"`
}

func New(cfg *config.Config, logger *slog.Logger, auth *keystone.Client, promClient *prom.Client, catalogManager *catalog.Manager) *Server {
	s := &Server{
		cfg:     cfg,
		logger:  logger,
		auth:    auth,
		prom:    promClient,
		catalog: catalogManager,
	}
	s.handler = s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) routes() http.Handler {
	router := chi.NewRouter()
	router.Use(s.loggingMiddleware)

	router.Get("/", s.handleRoot)
	router.Get("/healthz", s.handleHealth)
	router.Get("/readyz", s.handleReady)
	router.Handle("/metrics", promhttp.Handler())

	router.Route("/v1", func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Get("/capabilities", s.handleCapabilities)
		r.Get("/status", s.handleStatus)

		r.Get("/archive_policy", s.handleArchivePolicies)
		r.Get("/archive_policy/{name}", s.handleArchivePolicy)
		r.MethodFunc(http.MethodPost, "/archive_policy", unsupportedHandler("archive policy writes are not supported"))
		r.MethodFunc(http.MethodPatch, "/archive_policy/{name}", unsupportedHandler("archive policy writes are not supported"))
		r.MethodFunc(http.MethodDelete, "/archive_policy/{name}", unsupportedHandler("archive policy writes are not supported"))
		r.MethodFunc(http.MethodGet, "/archive_policy_rule", unsupportedHandler("archive policy rules are not supported"))
		r.MethodFunc(http.MethodPost, "/archive_policy_rule", unsupportedHandler("archive policy rules are not supported"))

		r.Get("/resource_type", s.handleResourceTypes)
		r.Get("/resource_type/{name}", s.handleResourceType)

		r.Get("/resource/{resourceType}", s.handleListResources)
		r.Get("/resource/{resourceType}/{resourceID}", s.handleGetResource)
		r.Post("/search/resource/{resourceType}", s.handleSearchResources)
		r.MethodFunc(http.MethodPost, "/resource/{resourceType}", unsupportedHandler("resource writes are not supported"))
		r.MethodFunc(http.MethodPatch, "/resource/{resourceType}/{resourceID}", unsupportedHandler("resource writes are not supported"))
		r.MethodFunc(http.MethodDelete, "/resource/{resourceType}/{resourceID}", unsupportedHandler("resource writes are not supported"))

		r.Get("/metric", s.handleListMetrics)
		r.Get("/metric/{metricID}", s.handleGetMetric)
		r.Get("/metric/{metricID}/measures", s.handleMetricMeasures)
		r.MethodFunc(http.MethodPost, "/metric", unsupportedHandler("metric writes are not supported"))
		r.MethodFunc(http.MethodDelete, "/metric/{metricID}", unsupportedHandler("metric writes are not supported"))
		r.MethodFunc(http.MethodPost, "/metric/{metricID}/measures", unsupportedHandler("measure ingestion is not supported"))

		r.Get("/resource/{resourceType}/{resourceID}/metric/{metricName}", s.handleGetResourceMetric)
		r.Get("/resource/{resourceType}/{resourceID}/metric/{metricName}/measures", s.handleResourceMetricMeasures)
		r.MethodFunc(http.MethodPost, "/resource/{resourceType}/{resourceID}/metric/{metricName}/measures", unsupportedHandler("measure ingestion is not supported"))

		r.Post("/aggregates", s.handleAggregates)
		r.MethodFunc(http.MethodPost, "/search/metric", unsupportedHandler("metric search is not supported"))
		r.MethodFunc(http.MethodPost, "/batch/metrics/measures", unsupportedHandler("batch measure ingestion is not supported"))
		r.MethodFunc(http.MethodPost, "/batch/resources/metrics/measures", unsupportedHandler("batch measure ingestion is not supported"))
	})

	return router
}

func unsupportedHandler(detail string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gnocchi.Unsupported(w, detail)
	}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"versions": map[string]any{
			"values": []map[string]any{
				{
					"id":     "v1",
					"status": "CURRENT",
					"links": []map[string]string{
						{
							"rel":  "self",
							"href": absoluteURL(r, "/v1/"),
						},
					},
				},
			},
		},
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if _, err := s.catalog.Snapshot(r.Context()); err != nil {
		gnocchi.WriteError(w, http.StatusServiceUnavailable, "Service Unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"aggregation_methods":   s.cfg.API.SupportedAggregations,
		"archive_policy_name":   "prometheus",
		"resource_types":        []string{"instance", "volume", "network", "port", "generic"},
		"supported_granularity": s.cfg.API.SupportedGranularities,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.catalog.Snapshot(r.Context())
	if err != nil {
		gnocchi.WriteError(w, http.StatusServiceUnavailable, "Service Unavailable", err.Error())
		return
	}
	resourceCounts := map[string]int{}
	for resourceType, resources := range snapshot.ResourcesByType {
		resourceCounts[resourceType] = len(resources)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"catalog": map[string]any{
			"last_refresh": snapshot.LastRefresh.Format(time.RFC3339),
		},
		"storage": map[string]any{
			"summary": map[string]any{
				"metrics":   len(snapshot.MetricsByID),
				"resources": resourceCounts,
			},
		},
	})
}

func (s *Server) handleArchivePolicies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []gnocchi.ArchivePolicy{s.syntheticArchivePolicy()})
}

func (s *Server) handleArchivePolicy(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "name") != "prometheus" {
		gnocchi.WriteError(w, http.StatusNotFound, "Not Found", "archive policy not found")
		return
	}
	writeJSON(w, http.StatusOK, s.syntheticArchivePolicy())
}

func (s *Server) syntheticArchivePolicy() gnocchi.ArchivePolicy {
	definitions := make([]gnocchi.ArchivePolicyDefinition, 0, len(s.cfg.API.SupportedGranularities))
	for _, granularity := range s.cfg.API.SupportedGranularities {
		duration, err := time.ParseDuration(granularity)
		if err != nil {
			continue
		}
		points := int((7 * 24 * time.Hour) / duration)
		if points < 1 {
			points = 1
		}
		definitions = append(definitions, gnocchi.ArchivePolicyDefinition{
			Granularity: granularity,
			Points:      points,
			Timespan:    (time.Duration(points) * duration).String(),
		})
	}
	return gnocchi.ArchivePolicy{
		Name:               "prometheus",
		AggregationMethods: append([]string(nil), s.cfg.API.SupportedAggregations...),
		BackWindow:         0,
		Definition:         definitions,
	}
}

func (s *Server) handleResourceTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, gnocchi.SupportedResourceTypes())
}

func (s *Server) handleResourceType(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	for _, resourceType := range gnocchi.SupportedResourceTypes() {
		if resourceType.Name == name {
			writeJSON(w, http.StatusOK, resourceType)
			return
		}
	}
	gnocchi.WriteError(w, http.StatusNotFound, "Not Found", "resource type not found")
}

func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	authCtx := authContext(r)
	resourceType := chi.URLParam(r, "resourceType")
	resources, err := s.resourcesForRequest(r.Context(), authCtx, resourceType)
	if err != nil {
		s.writeErr(w, err)
		return
	}

	filter, err := parseResourceFilter(r, nil)
	if err != nil {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}
	resources = applyResourceFilter(resources, filter)
	resources = sortResources(resources, parseSorts(r.URL.Query()["sort"]))
	page, nextMarker := paginateResources(resources, r.URL.Query().Get("marker"), parseLimit(r))
	if nextMarker != "" {
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextPageURL(r, nextMarker)))
	}

	attrs := r.URL.Query()["attrs"]
	response := make([]map[string]any, 0, len(page))
	for _, resource := range page {
		response = append(response, resource.ToResponse(attrs))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
	authCtx := authContext(r)
	resourceType := chi.URLParam(r, "resourceType")
	resourceID := chi.URLParam(r, "resourceID")

	resource, err := s.resourceByID(r.Context(), authCtx, resourceType, resourceID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource.ToResponse(r.URL.Query()["attrs"]))
}

func (s *Server) handleSearchResources(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("history") == "true" {
		gnocchi.Unsupported(w, "resource history is not supported")
		return
	}

	body, err := readBody(r)
	if err != nil {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}
	filter, err := parseResourceFilter(r, body)
	if err != nil {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}

	authCtx := authContext(r)
	resourceType := chi.URLParam(r, "resourceType")
	resources, err := s.resourcesForRequest(r.Context(), authCtx, resourceType)
	if err != nil {
		s.writeErr(w, err)
		return
	}

	resources = applyResourceFilter(resources, filter)
	resources = sortResources(resources, parseSorts(r.URL.Query()["sort"]))
	page, nextMarker := paginateResources(resources, r.URL.Query().Get("marker"), parseLimit(r))
	if nextMarker != "" {
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextPageURL(r, nextMarker)))
	}

	response := make([]map[string]any, 0, len(page))
	for _, resource := range page {
		response = append(response, resource.ToResponse(r.URL.Query()["attrs"]))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleListMetrics(w http.ResponseWriter, r *http.Request) {
	metrics, err := s.listMetrics(r.Context(), authContext(r))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].ID < metrics[j].ID })
	page, nextMarker := paginateMetrics(metrics, r.URL.Query().Get("marker"), parseLimit(r))
	if nextMarker != "" {
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextPageURL(r, nextMarker)))
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleGetMetric(w http.ResponseWriter, r *http.Request) {
	metric, err := s.metricByID(r.Context(), authContext(r), chi.URLParam(r, "metricID"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metric)
}

func (s *Server) handleGetResourceMetric(w http.ResponseWriter, r *http.Request) {
	metric, err := s.metricByResourceAndName(r.Context(), authContext(r), chi.URLParam(r, "resourceType"), chi.URLParam(r, "resourceID"), chi.URLParam(r, "metricName"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metric)
}

func (s *Server) handleMetricMeasures(w http.ResponseWriter, r *http.Request) {
	metric, err := s.metricByID(r.Context(), authContext(r), chi.URLParam(r, "metricID"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	measures, err := s.queryMeasures(r.Context(), metric.ResourceType, metric.ResourceID, metric.Name, r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gnocchi.MeasuresResponse(measures))
}

func (s *Server) handleResourceMetricMeasures(w http.ResponseWriter, r *http.Request) {
	if _, err := s.metricByResourceAndName(r.Context(), authContext(r), chi.URLParam(r, "resourceType"), chi.URLParam(r, "resourceID"), chi.URLParam(r, "metricName")); err != nil {
		s.writeErr(w, err)
		return
	}
	measures, err := s.queryMeasures(r.Context(), chi.URLParam(r, "resourceType"), chi.URLParam(r, "resourceID"), chi.URLParam(r, "metricName"), r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gnocchi.MeasuresResponse(measures))
}

func (s *Server) handleAggregates(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()["groupby"]) > 0 {
		gnocchi.Unsupported(w, "aggregate groupby is not supported in v1")
		return
	}

	body, err := readBody(r)
	if err != nil {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}
	var request AggregateRequest
	if err := json.Unmarshal(body, &request); err != nil {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}

	if request.ResourceType == "" {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", "resource_type is required")
		return
	}

	operationExpr, err := parseAggregateOperations(request.Operations)
	if err != nil {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}

	filter, err := parseAggregateSearch(request.Search)
	if err != nil {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}

	authCtx := authContext(r)
	resources, err := s.resourcesForRequest(r.Context(), authCtx, request.ResourceType)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	resources = applyResourceFilter(resources, filter)

	params, err := s.measureParamsFromRequest(r)
	if err != nil {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}
	value, err := s.evaluateAggregate(r.Context(), request.ResourceType, resources, operationExpr, params)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if value.Kind != valueKindSeries {
		gnocchi.WriteError(w, http.StatusBadRequest, "Bad Request", "aggregate operations must evaluate to a time series")
		return
	}
	writeJSON(w, http.StatusOK, gnocchi.MeasuresResponse(seriesToMeasures(value.Series, params.OutputGranularity)))
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("request complete", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Auth-Token")
		if token == "" {
			gnocchi.WriteError(w, http.StatusUnauthorized, "Unauthorized", "missing X-Auth-Token")
			return
		}

		authCtx, err := s.auth.ValidateToken(r.Context(), token)
		if err != nil {
			gnocchi.WriteError(w, http.StatusUnauthorized, "Unauthorized", err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey, authCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authContext(r *http.Request) gnocchi.Context {
	value := r.Context().Value(authContextKey)
	if ctx, ok := value.(gnocchi.Context); ok {
		return ctx
	}
	return gnocchi.Context{}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, path)
}

func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return ioReadAllLimit(r.Body, 1<<20)
}

func parseLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0
	}
	return limit
}

func nextPageURL(r *http.Request, marker string) string {
	query := r.URL.Query()
	query.Set("marker", marker)
	copyURL := *r.URL
	copyURL.RawQuery = query.Encode()
	return absoluteURL(r, copyURL.String())
}

func parseResourceFilter(r *http.Request, body []byte) (catalog.Predicate, error) {
	if filter := strings.TrimSpace(r.URL.Query().Get("filter")); filter != "" {
		return catalog.ParseFlatFilter(filter)
	}
	return catalog.ParseJSONFilter(body)
}

func parseAggregateSearch(search json.RawMessage) (catalog.Predicate, error) {
	if len(search) == 0 {
		return catalog.PredicateFunc(func(*gnocchi.Resource) bool { return true }), nil
	}
	if len(search) > 0 && search[0] == '"' {
		var filter string
		if err := json.Unmarshal(search, &filter); err != nil {
			return nil, err
		}
		return catalog.ParseFlatFilter(filter)
	}
	return catalog.ParseJSONFilter(search)
}

type sortSpec struct {
	Field string
	Desc  bool
}

func parseSorts(raw []string) []sortSpec {
	specs := make([]sortSpec, 0)
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			spec := sortSpec{Field: part}
			if strings.Contains(part, ":") {
				pieces := strings.SplitN(part, ":", 2)
				spec.Field = pieces[0]
				spec.Desc = strings.EqualFold(pieces[1], "desc")
			}
			specs = append(specs, spec)
		}
	}
	return specs
}

func sortResources(resources []*gnocchi.Resource, sorts []sortSpec) []*gnocchi.Resource {
	clones := append([]*gnocchi.Resource(nil), resources...)
	sort.SliceStable(clones, func(i, j int) bool {
		for _, spec := range sorts {
			left := compareValue(resourceField(clones[i], spec.Field))
			right := compareValue(resourceField(clones[j], spec.Field))
			if left == right {
				continue
			}
			if spec.Desc {
				return left > right
			}
			return left < right
		}
		return clones[i].ID < clones[j].ID
	})
	return clones
}

func resourceField(resource *gnocchi.Resource, field string) any {
	switch field {
	case "id":
		return resource.ID
	case "type":
		return resource.Type
	default:
		return resource.Attrs[field]
	}
}

func compareValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func paginateResources(resources []*gnocchi.Resource, marker string, limit int) ([]*gnocchi.Resource, string) {
	start := 0
	if marker != "" {
		for index, resource := range resources {
			if resource.ID == marker {
				start = index + 1
				break
			}
		}
	}
	if limit == 0 || start+limit >= len(resources) {
		return resources[start:], ""
	}
	return resources[start : start+limit], resources[start+limit-1].ID
}

func paginateMetrics(metrics []*gnocchi.Metric, marker string, limit int) ([]*gnocchi.Metric, string) {
	start := 0
	if marker != "" {
		for index, metric := range metrics {
			if metric.ID == marker {
				start = index + 1
				break
			}
		}
	}
	if limit == 0 || start+limit >= len(metrics) {
		return metrics[start:], ""
	}
	return metrics[start : start+limit], metrics[start+limit-1].ID
}

func applyResourceFilter(resources []*gnocchi.Resource, filter catalog.Predicate) []*gnocchi.Resource {
	if filter == nil {
		return resources
	}
	filtered := make([]*gnocchi.Resource, 0, len(resources))
	for _, resource := range resources {
		if filter.Match(resource) {
			filtered = append(filtered, resource)
		}
	}
	return filtered
}

func (s *Server) writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gnocchi.ErrNotFound):
		gnocchi.WriteError(w, http.StatusNotFound, "Not Found", err.Error())
	default:
		gnocchi.WriteError(w, http.StatusBadGateway, "Bad Gateway", err.Error())
	}
}
