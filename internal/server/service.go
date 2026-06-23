package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/catalog"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/gnocchi"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/prom"
)

type measureParams struct {
	Start             time.Time
	End               time.Time
	Step              time.Duration
	Window            string
	OutputGranularity string
	Aggregation       string
}

func (s *Server) resourcesForRequest(ctx context.Context, authCtx gnocchi.Context, resourceType string) ([]*gnocchi.Resource, error) {
	snapshot, err := s.catalog.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	resources, ok := snapshot.ResourcesByType[resourceType]
	if !ok {
		return nil, fmt.Errorf("%w: resource type %s", gnocchi.ErrNotFound, resourceType)
	}
	filtered := make([]*gnocchi.Resource, 0, len(resources))
	for _, resource := range resources {
		if authCtx.IsAdmin {
			filtered = append(filtered, resource.Clone())
			continue
		}
		projectID := fmt.Sprint(resource.Attrs["project_id"])
		if projectID != "" && projectID == authCtx.ProjectID {
			filtered = append(filtered, resource.Clone())
		}
	}
	return filtered, nil
}

func (s *Server) resourceByID(ctx context.Context, authCtx gnocchi.Context, resourceType, resourceID string) (*gnocchi.Resource, error) {
	resources, err := s.resourcesForRequest(ctx, authCtx, resourceType)
	if err != nil {
		return nil, err
	}
	for _, resource := range resources {
		if resource.ID == resourceID {
			return resource, nil
		}
	}
	return nil, fmt.Errorf("%w: resource %s", gnocchi.ErrNotFound, resourceID)
}

func (s *Server) listMetrics(ctx context.Context, authCtx gnocchi.Context) ([]*gnocchi.Metric, error) {
	snapshot, err := s.catalog.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	resourceAccess := map[string]bool{}
	for resourceType, resources := range snapshot.ResourcesByType {
		for _, resource := range resources {
			if authCtx.IsAdmin || fmt.Sprint(resource.Attrs["project_id"]) == authCtx.ProjectID {
				resourceAccess[resourceType+"/"+resource.ID] = true
			}
		}
	}
	metrics := make([]*gnocchi.Metric, 0, len(snapshot.MetricsByID))
	for _, metric := range snapshot.MetricsByID {
		if resourceAccess[metric.ResourceType+"/"+metric.ResourceID] {
			copyMetric := *metric
			metrics = append(metrics, &copyMetric)
		}
	}
	return metrics, nil
}

func (s *Server) metricByID(ctx context.Context, authCtx gnocchi.Context, metricID string) (*gnocchi.Metric, error) {
	snapshot, err := s.catalog.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	metric, ok := snapshot.MetricsByID[metricID]
	if !ok {
		return nil, fmt.Errorf("%w: metric %s", gnocchi.ErrNotFound, metricID)
	}
	if _, err := s.resourceByID(ctx, authCtx, metric.ResourceType, metric.ResourceID); err != nil {
		return nil, err
	}
	copyMetric := *metric
	return &copyMetric, nil
}

func (s *Server) metricByResourceAndName(ctx context.Context, authCtx gnocchi.Context, resourceType, resourceID, metricName string) (*gnocchi.Metric, error) {
	resource, err := s.resourceByID(ctx, authCtx, resourceType, resourceID)
	if err != nil {
		return nil, err
	}
	metric, ok := resource.Metrics[metricName]
	if !ok {
		return nil, fmt.Errorf("%w: metric %s", gnocchi.ErrNotFound, metricName)
	}
	copyMetric := *metric
	return &copyMetric, nil
}

func (s *Server) measureParamsFromRequest(r *http.Request) (measureParams, error) {
	granularity := r.URL.Query().Get("granularity")
	resample := r.URL.Query().Get("resample")
	outputGranularity := s.cfg.API.DefaultGranularity
	if resample != "" {
		outputGranularity = resample
	} else if granularity != "" {
		outputGranularity = granularity
	}

	step, err := parsePromStep(outputGranularity)
	if err != nil {
		return measureParams{}, err
	}

	end := time.Now().UTC()
	if raw := r.URL.Query().Get("stop"); raw != "" {
		end, err = parseFlexibleTime(raw)
		if err != nil {
			return measureParams{}, fmt.Errorf("parse stop: %w", err)
		}
	}

	start := end.Add(-24 * step)
	if raw := r.URL.Query().Get("start"); raw != "" {
		start, err = parseFlexibleTime(raw)
		if err != nil {
			return measureParams{}, fmt.Errorf("parse start: %w", err)
		}
	}
	if !start.Before(end) {
		return measureParams{}, fmt.Errorf("start must be before stop")
	}

	aggregation := r.URL.Query().Get("aggregation")
	if aggregation == "" {
		aggregation = s.cfg.API.DefaultAggregation
	}
	if !containsFold(s.cfg.API.SupportedAggregations, aggregation) {
		return measureParams{}, fmt.Errorf("unsupported aggregation %q", aggregation)
	}

	return measureParams{
		Start:             start,
		End:               end,
		Step:              step,
		Window:            promWindow(outputGranularity),
		OutputGranularity: outputGranularity,
		Aggregation:       strings.ToLower(aggregation),
	}, nil
}

func promWindow(granularity string) string {
	switch strings.ToUpper(granularity) {
	case "W":
		return "168h"
	case "M":
		return "720h"
	case "Q":
		return "2160h"
	case "H":
		return "4380h"
	case "Y":
		return "8760h"
	default:
		return granularity
	}
}

func parsePromStep(granularity string) (time.Duration, error) {
	switch strings.ToUpper(granularity) {
	case "W":
		return 7 * 24 * time.Hour, nil
	case "M":
		return 30 * 24 * time.Hour, nil
	case "Q":
		return 90 * 24 * time.Hour, nil
	case "H":
		return 182 * 24 * time.Hour, nil
	case "Y":
		return 365 * 24 * time.Hour, nil
	default:
		return time.ParseDuration(granularity)
	}
}

func parseFlexibleTime(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", raw)
}

func (s *Server) queryMeasures(ctx context.Context, resourceType, resourceID, metricName string, r *http.Request) ([]gnocchi.Measure, error) {
	params, err := s.measureParamsFromRequest(r)
	if err != nil {
		return nil, err
	}
	return s.queryMeasuresWithParams(ctx, resourceType, resourceID, metricName, params.Aggregation, params)
}

func (s *Server) queryMeasuresWithParams(ctx context.Context, resourceType, resourceID, metricName, aggregation string, params measureParams) ([]gnocchi.Measure, error) {
	definition, ok := catalog.DefinitionByName(resourceType, metricName)
	if !ok {
		return nil, fmt.Errorf("%w: metric %s", gnocchi.ErrNotFound, metricName)
	}

	selectors := catalog.Selectors{
		Libvirt:  strings.TrimSpace(s.cfg.Prometheus.LibvirtSelector),
		Database: strings.TrimSpace(s.cfg.Prometheus.DatabaseSelector),
	}

	switch definition.Mode {
	case catalog.QueryModeRangeFunction:
		expr := definition.ValueQuery(resourceID, selectors, aggregation, params.Window)
		streams, err := s.prom.QueryRange(ctx, expr, params.Start, params.End, params.Step)
		if err != nil {
			return nil, err
		}
		return rangeStreamsToMeasures(streams, params.OutputGranularity), nil
	case catalog.QueryModePresenceCount:
		expr := fmt.Sprintf("count(%s)", definition.RawQuery(resourceID, selectors))
		streams, err := s.prom.QueryRange(ctx, expr, params.Start, params.End, params.Step)
		if err != nil {
			return nil, err
		}
		return rangeStreamsToMeasures(streams, params.OutputGranularity), nil
	case catalog.QueryModeLabelStatus:
		streams, err := s.prom.QueryRange(ctx, definition.RawQuery(resourceID, selectors), params.Start, params.End, params.Step)
		if err != nil {
			return nil, err
		}
		return labelStatusMeasures(streams, definition.SelectorKey, definition.StatusMap, params.OutputGranularity), nil
	default:
		return nil, fmt.Errorf("unsupported query mode %s", definition.Mode)
	}
}

func rangeStreamsToMeasures(streams []prom.SampleStream, granularity string) []gnocchi.Measure {
	series := map[int64]float64{}
	for _, stream := range streams {
		for _, point := range stream.Values {
			series[point.Timestamp.Unix()] += point.Value
		}
	}
	return seriesToMeasures(series, granularity)
}

func labelStatusMeasures(streams []prom.SampleStream, selectorKey string, mapping map[string]float64, granularity string) []gnocchi.Measure {
	series := map[int64]float64{}
	for _, stream := range streams {
		status := stream.Metric[selectorKey]
		value, ok := mapping[strings.ToUpper(status)]
		if !ok {
			value = mapping["UNKNOWN"]
		}
		for _, point := range stream.Values {
			series[point.Timestamp.Unix()] = value
		}
	}
	return seriesToMeasures(series, granularity)
}

func seriesToMeasures(series map[int64]float64, granularity string) []gnocchi.Measure {
	timestamps := make([]int64, 0, len(series))
	for ts := range series {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	measures := make([]gnocchi.Measure, 0, len(timestamps))
	for _, ts := range timestamps {
		measures = append(measures, gnocchi.Measure{
			Timestamp:   time.Unix(ts, 0).UTC(),
			Granularity: granularity,
			Value:       series[ts],
		})
	}
	return measures
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func ioReadAllLimit(r io.Reader, limit int64) ([]byte, error) {
	limited := io.LimitReader(r, limit)
	return io.ReadAll(limited)
}
