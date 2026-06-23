package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"log/slog"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/catalog"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/config"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/keystone"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/prom"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/server"
)

func TestAPIResourceScopingAndSearch(t *testing.T) {
	t.Parallel()

	env := newTestEnvironment(t)

	resp := env.do(t, http.MethodGet, "/v1/resource/instance", nil, "user-token-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var resources []map[string]any
	decodeJSON(t, resp, &resources)
	if len(resources) != 1 {
		t.Fatalf("expected one project-scoped instance, got %d", len(resources))
	}

	resp = env.do(t, http.MethodGet, "/v1/resource/instance", nil, "admin-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &resources)
	if len(resources) != 2 {
		t.Fatalf("expected admin to see two instances, got %d", len(resources))
	}

	resp = env.do(t, http.MethodPost, "/v1/search/resource/instance", bytes.NewBufferString(`{"=":{"display_name":"vm-a"}}`), "user-token-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &resources)
	if len(resources) != 1 || resources[0]["display_name"] != "vm-a" {
		t.Fatalf("unexpected search result: %#v", resources)
	}

	resp = env.do(t, http.MethodGet, "/v1/resource_type/instance", nil, "admin-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var resourceType map[string]any
	decodeJSON(t, resp, &resourceType)
	attributes, ok := resourceType["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected resource type attributes map for gnocchiclient compatibility, got %#v", resourceType)
	}
	displayName, ok := attributes["display_name"].(map[string]any)
	if !ok || displayName["type"] != "string" {
		t.Fatalf("unexpected resource type attributes: %#v", attributes)
	}
}

func TestAPIMetricLookupAndMeasures(t *testing.T) {
	t.Parallel()

	env := newTestEnvironment(t)

	resp := env.do(t, http.MethodGet, "/v1/resource/instance/instance-a/metric/cpu.time", nil, "user-token-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var metric map[string]any
	decodeJSON(t, resp, &metric)
	if metric["name"] != "cpu.time" {
		t.Fatalf("unexpected metric payload: %#v", metric)
	}
	archivePolicy, ok := metric["archive_policy"].(map[string]any)
	if !ok || archivePolicy["name"] != "prometheus" {
		t.Fatalf("expected nested archive_policy, got %#v", metric)
	}
	if _, ok := archivePolicy["definition"].([]any); !ok {
		t.Fatalf("expected archive policy definition, got %#v", archivePolicy)
	}
	if _, exists := metric["created_by_user_id"]; !exists {
		t.Fatalf("expected created_by_user_id key, got %#v", metric)
	}
	if _, exists := metric["created_by_project_id"]; !exists {
		t.Fatalf("expected created_by_project_id key, got %#v", metric)
	}

	resp = env.do(t, http.MethodGet, "/v1/resource/instance/instance-a/metric/cpu_util", nil, "user-token-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &metric)
	if metric["name"] != "cpu_util" {
		t.Fatalf("unexpected metric payload: %#v", metric)
	}
	archivePolicy, ok = metric["archive_policy"].(map[string]any)
	if !ok || archivePolicy["name"] != "prometheus" {
		t.Fatalf("expected nested archive_policy, got %#v", metric)
	}

	resp = env.do(t, http.MethodGet, "/v1/metric", nil, "user-token-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var metrics []map[string]any
	decodeJSON(t, resp, &metrics)
	if len(metrics) == 0 {
		t.Fatalf("expected at least one metric")
	}
	archivePolicy, ok = metrics[0]["archive_policy"].(map[string]any)
	if !ok || archivePolicy["name"] != "prometheus" {
		t.Fatalf("expected metric list to include full archive_policy, got %#v", metrics[0])
	}
	if _, ok := archivePolicy["definition"].([]any); !ok {
		t.Fatalf("expected metric list archive policy definition, got %#v", archivePolicy)
	}

	resp = env.do(t, http.MethodGet, "/v1/resource/instance/instance-a/metric/cpu.time/measures?start=2024-01-01T00:00:00Z&stop=2024-01-01T00:02:00Z&granularity=60s&aggregation=max", nil, "user-token-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var measures [][]any
	decodeJSON(t, resp, &measures)
	if len(measures) != 2 {
		t.Fatalf("expected two measures, got %d", len(measures))
	}
	if !env.prometheus.sawRangeQuery("max_over_time") {
		t.Fatalf("expected Prometheus range query to use max_over_time, got %v", env.prometheus.rangeQueries())
	}

	resp = env.do(t, http.MethodGet, "/v1/resource/instance/instance-a/metric/cpu_util/measures?start=2024-01-01T00:00:00Z&stop=2024-01-01T00:02:00Z&granularity=60s&aggregation=mean", nil, "user-token-a")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &measures)
	if len(measures) != 2 {
		t.Fatalf("expected two measures, got %d", len(measures))
	}
	if !env.prometheus.sawRangeQuery("rate(libvirt_domain_info_cpu_time_seconds_total") {
		t.Fatalf("expected cpu_util query to use cpu time rate, got %v", env.prometheus.rangeQueries())
	}
	if !env.prometheus.sawRangeQuery("libvirt_domain_vcpu_current") {
		t.Fatalf("expected cpu_util query to include vcpu capacity, got %v", env.prometheus.rangeQueries())
	}
}

func TestAPIAggregatesAndUnsupportedEndpoints(t *testing.T) {
	t.Parallel()

	env := newTestEnvironment(t)

	resp := env.do(t, http.MethodPost, "/v1/aggregates?start=2024-01-01T00:00:00Z&stop=2024-01-01T00:02:00Z&granularity=60s", bytes.NewBufferString(`{
  "operations": ["*", ["aggregate", "mean", ["metric", "cpu.time", "mean"]], 4],
  "resource_type": "instance",
  "search": {"like": {"display_name": "vm-%"}}
}`), "admin-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var measures [][]any
	decodeJSON(t, resp, &measures)
	if got := measures[0][2].(float64); got != 6 {
		t.Fatalf("unexpected first aggregate value %v", got)
	}
	if got := measures[1][2].(float64); got != 14 {
		t.Fatalf("unexpected second aggregate value %v", got)
	}

	resp = env.do(t, http.MethodPost, "/v1/search/metric", bytes.NewBufferString(`{}`), "admin-token")
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", resp.StatusCode)
	}

	resp = env.do(t, http.MethodGet, "/v1/status", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing token, got %d", resp.StatusCode)
	}

	resp = env.do(t, http.MethodGet, "/v1/status", nil, "admin-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var status map[string]any
	decodeJSON(t, resp, &status)
	storage, ok := status["storage"].(map[string]any)
	if !ok {
		t.Fatalf("expected storage summary, got %#v", status)
	}
	summary, ok := storage["summary"].(map[string]any)
	if !ok {
		t.Fatalf("expected storage summary, got %#v", storage)
	}
	if _, ok := summary["measures"]; !ok {
		t.Fatalf("expected measures count for gnocchiclient status compatibility, got %#v", summary)
	}
}

type testEnvironment struct {
	handler    http.Handler
	prometheus *fakePrometheus
}

func newTestEnvironment(t *testing.T) *testEnvironment {
	t.Helper()

	prometheus := newFakePrometheus()
	promServer := httptest.NewServer(prometheus)
	t.Cleanup(promServer.Close)

	keystoneServer := httptest.NewServer(newFakeKeystone())
	t.Cleanup(keystoneServer.Close)

	cfg := config.Default()
	cfg.Prometheus.BaseURL = promServer.URL
	cfg.Prometheus.QueryTimeout = 5 * time.Second
	cfg.Keystone.AuthURL = keystoneServer.URL
	cfg.Keystone.Username = "svc"
	cfg.Keystone.Password = "secret"
	cfg.Keystone.ProjectName = "service"
	cfg.Keystone.UserDomainName = "Default"
	cfg.Keystone.ProjectDomainName = "Default"

	logger := slog.New(slog.NewTextHandler(&discardWriter{}, nil))
	promClient, err := prom.New(cfg.Prometheus.BaseURL, cfg.Prometheus.QueryTimeout, nil, false)
	if err != nil {
		t.Fatalf("new prometheus client: %v", err)
	}
	authClient, err := keystone.New(cfg.Keystone)
	if err != nil {
		t.Fatalf("new keystone client: %v", err)
	}
	catalogManager := catalog.NewManager(cfg, promClient)
	if err := catalogManager.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh catalog: %v", err)
	}

	return &testEnvironment{
		handler:    server.New(cfg, logger, authClient, promClient, catalogManager).Handler(),
		prometheus: prometheus,
	}
}

func (e *testEnvironment) do(t *testing.T, method, path string, body *bytes.Buffer, token string) *http.Response {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body.Bytes())
	}

	req := httptest.NewRequest(method, path, reader)
	req.Host = "gnocchi.test"
	if token != "" {
		req.Header.Set("X-Auth-Token", token)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec.Result()
}

func decodeJSON[T any](t *testing.T, resp *http.Response, target *T) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

type fakePrometheus struct {
	mu        sync.Mutex
	rangeSeen []string
}

func newFakePrometheus() *fakePrometheus {
	return &fakePrometheus{}
}

func (f *fakePrometheus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	switch r.URL.Path {
	case "/api/v1/query":
		f.writeInstant(w, query)
	case "/api/v1/query_range":
		f.mu.Lock()
		f.rangeSeen = append(f.rangeSeen, query)
		f.mu.Unlock()
		f.writeRange(w, query)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakePrometheus) rangeQueries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.rangeSeen...)
}

func (f *fakePrometheus) sawRangeQuery(fragment string) bool {
	for _, query := range f.rangeQueries() {
		if strings.Contains(query, fragment) {
			return true
		}
	}
	return false
}

func (f *fakePrometheus) writeInstant(w http.ResponseWriter, query string) {
	type result struct {
		Metric map[string]string `json:"metric"`
		Value  []any             `json:"value"`
	}
	results := []result{}

	switch {
	case strings.Contains(query, "openstack_nova_server_status"):
		results = []result{
			{Metric: map[string]string{"id": "instance-a", "uuid": "instance-a", "name": "vm-a", "tenant_id": "project-a", "user_id": "user-a", "hypervisor_hostname": "compute1", "flavor_id": "m1.small", "availability_zone": "az1", "status": "ACTIVE"}, Value: []any{float64(1704067200), "1"}},
			{Metric: map[string]string{"id": "instance-b", "uuid": "instance-b", "name": "vm-b", "tenant_id": "project-b", "user_id": "user-b", "hypervisor_hostname": "compute2", "flavor_id": "m1.medium", "availability_zone": "az1", "status": "ACTIVE"}, Value: []any{float64(1704067200), "2"}},
		}
	case strings.Contains(query, "openstack_cinder_volume_gb"):
		results = []result{
			{Metric: map[string]string{"id": "volume-a", "name": "data-a", "tenant_id": "project-a", "user_id": "user-a", "status": "available", "server_id": "instance-a", "volume_type": "fast", "bootable": "false"}, Value: []any{float64(1704067200), "10"}},
		}
	case strings.Contains(query, "openstack_neutron_network"):
		results = []result{
			{Metric: map[string]string{"id": "network-a", "name": "net-a", "tenant_id": "project-a", "status": "ACTIVE", "is_external": "false", "is_shared": "false", "provider_network_type": "vxlan", "provider_physical_network": "", "provider_segmentation_id": "1001", "subnets": "subnet-a", "tags": ""}, Value: []any{float64(1704067200), "0"}},
		}
	case strings.Contains(query, "openstack_neutron_port"):
		results = []result{
			{Metric: map[string]string{"uuid": "port-a", "network_id": "network-a", "status": "ACTIVE", "device_owner": "compute:nova", "fixed_ips": "10.0.0.10", "mac_address": "fa:16:3e:00:00:01", "admin_state_up": "true", "binding_vif_type": "ovs"}, Value: []any{float64(1704067200), "1"}},
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result":     results,
		},
	})
}

func (f *fakePrometheus) writeRange(w http.ResponseWriter, query string) {
	type result struct {
		Metric map[string]string `json:"metric"`
		Values [][]any           `json:"values"`
	}

	series := []result{}
	switch {
	case strings.Contains(query, "instance_id=\"instance-a\""):
		series = []result{{Metric: map[string]string{"instance_id": "instance-a"}, Values: [][]any{{float64(1704067200), "1"}, {float64(1704067260), "3"}}}}
	case strings.Contains(query, "instance_id=\"instance-b\""):
		series = []result{{Metric: map[string]string{"instance_id": "instance-b"}, Values: [][]any{{float64(1704067200), "2"}, {float64(1704067260), "4"}}}}
	case strings.Contains(query, "openstack_cinder_volume_gb") && strings.Contains(query, `id="volume-a"`):
		series = []result{{Metric: map[string]string{"id": "volume-a"}, Values: [][]any{{float64(1704067200), "10"}, {float64(1704067260), "10"}}}}
	case strings.Contains(query, "count(openstack_neutron_network") && strings.Contains(query, `id="network-a"`):
		series = []result{{Metric: map[string]string{"id": "network-a"}, Values: [][]any{{float64(1704067200), "1"}, {float64(1704067260), "1"}}}}
	case strings.Contains(query, "openstack_neutron_network") && strings.Contains(query, `id="network-a"`):
		series = []result{{Metric: map[string]string{"id": "network-a", "status": "ACTIVE"}, Values: [][]any{{float64(1704067200), "0"}, {float64(1704067260), "0"}}}}
	case strings.Contains(query, "openstack_neutron_port") && strings.Contains(query, `uuid="port-a"`):
		series = []result{{Metric: map[string]string{"uuid": "port-a", "status": "ACTIVE"}, Values: [][]any{{float64(1704067200), "1"}, {float64(1704067260), "1"}}}}
	case strings.Contains(query, "openstack_nova_server_status") && strings.Contains(query, `id="instance-a"`):
		series = []result{{Metric: map[string]string{"id": "instance-a"}, Values: [][]any{{float64(1704067200), "1"}, {float64(1704067260), "1"}}}}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result":     series,
		},
	})
}

func newFakeKeystone() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/auth/tokens":
			w.Header().Set("X-Subject-Token", "service-token")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": map[string]any{
					"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v3/auth/tokens":
			token := r.Header.Get("X-Subject-Token")
			switch token {
			case "user-token-a":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"token": map[string]any{
						"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
						"user":       map[string]any{"id": "user-a"},
						"project":    map[string]any{"id": "project-a"},
						"roles":      []map[string]any{{"name": "member"}},
					},
				})
			case "admin-token":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"token": map[string]any{
						"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
						"user":       map[string]any{"id": "user-admin"},
						"project":    map[string]any{"id": "service"},
						"roles":      []map[string]any{{"name": "admin"}},
					},
				})
			default:
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized"})
			}
		default:
			http.NotFound(w, r)
		}
	})
}
