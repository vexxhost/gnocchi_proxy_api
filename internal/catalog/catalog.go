package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/config"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/gnocchi"
	"github.com/yaguang-tang/gnocchi-proxy-api/internal/prom"
)

type Snapshot struct {
	ResourcesByType map[string][]*gnocchi.Resource
	MetricsByID     map[string]*gnocchi.Metric
	ResourceIndex   map[string]map[string]*gnocchi.Resource
	LastRefresh     time.Time
}

type Manager struct {
	cfg     *config.Config
	client  *prom.Client
	mu      sync.RWMutex
	current Snapshot
	lastErr error
}

func NewManager(cfg *config.Config, client *prom.Client) *Manager {
	return &Manager{
		cfg:    cfg,
		client: client,
	}
}

func (m *Manager) Start(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.Catalog.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.Refresh(ctx)
		}
	}
}

func (m *Manager) Refresh(ctx context.Context) error {
	snapshot, err := m.buildSnapshot(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.lastErr = err
		return err
	}
	m.current = snapshot
	m.lastErr = nil
	return nil
}

func (m *Manager) Snapshot(ctx context.Context) (Snapshot, error) {
	m.mu.RLock()
	current := m.current
	lastErr := m.lastErr
	m.mu.RUnlock()
	if !current.LastRefresh.IsZero() {
		return current, lastErr
	}
	if err := m.Refresh(ctx); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current, m.lastErr
}

func (m *Manager) buildSnapshot(ctx context.Context) (Snapshot, error) {
	selectors := Selectors{
		Libvirt:  strings.TrimSpace(m.cfg.Prometheus.LibvirtSelector),
		Database: strings.TrimSpace(m.cfg.Prometheus.DatabaseSelector),
	}

	instanceSamples, err := m.client.Query(ctx, metricSelector("openstack_nova_server_status", selectors.Database, "", ""), time.Time{})
	if err != nil {
		return Snapshot{}, fmt.Errorf("query instance catalog: %w", err)
	}
	volumeSamples, err := m.client.Query(ctx, metricSelector("openstack_cinder_volume_gb", selectors.Database, "", ""), time.Time{})
	if err != nil {
		return Snapshot{}, fmt.Errorf("query volume catalog: %w", err)
	}
	networkSamples, err := m.client.Query(ctx, metricSelector("openstack_neutron_network", selectors.Database, "", ""), time.Time{})
	if err != nil {
		return Snapshot{}, fmt.Errorf("query network catalog: %w", err)
	}
	portSamples, err := m.client.Query(ctx, metricSelector("openstack_neutron_port", selectors.Database, "", ""), time.Time{})
	if err != nil {
		return Snapshot{}, fmt.Errorf("query port catalog: %w", err)
	}

	resourcesByType := map[string][]*gnocchi.Resource{
		"instance": buildInstances(instanceSamples, m.cfg.API.SupportedAggregations),
		"volume":   buildVolumes(volumeSamples, m.cfg.API.SupportedAggregations),
		"network":  buildNetworks(networkSamples, m.cfg.API.SupportedAggregations),
	}
	resourcesByType["port"] = buildPorts(portSamples, resourcesByType["network"], m.cfg.API.SupportedAggregations)
	resourcesByType["generic"] = buildGeneric(resourcesByType, m.cfg.API.SupportedAggregations)

	snapshot := Snapshot{
		ResourcesByType: resourcesByType,
		MetricsByID:     map[string]*gnocchi.Metric{},
		ResourceIndex:   map[string]map[string]*gnocchi.Resource{},
		LastRefresh:     time.Now().UTC(),
	}

	for resourceType, resources := range resourcesByType {
		index := make(map[string]*gnocchi.Resource, len(resources))
		sort.Slice(resources, func(i, j int) bool { return resources[i].ID < resources[j].ID })
		for _, resource := range resources {
			index[resource.ID] = resource
			for _, metric := range resource.Metrics {
				snapshot.MetricsByID[metric.ID] = metric
			}
		}
		snapshot.ResourceIndex[resourceType] = index
	}

	return snapshot, nil
}

func buildInstances(samples []prom.Sample, aggregations []string) []*gnocchi.Resource {
	out := make([]*gnocchi.Resource, 0, len(samples))
	for _, sample := range samples {
		id := sample.Metric["uuid"]
		if id == "" {
			id = sample.Metric["id"]
		}
		if id == "" {
			continue
		}
		resource := &gnocchi.Resource{
			ID:   id,
			Type: "instance",
			Attrs: mergeAttrs(commonResourceAttrs(id, sample.Timestamp, sample.Metric["tenant_id"], sample.Metric["user_id"]), map[string]any{
				"display_name":      sample.Metric["name"],
				"host":              sample.Metric["hypervisor_hostname"],
				"flavor_id":         sample.Metric["flavor_id"],
				"availability_zone": sample.Metric["availability_zone"],
				"status":            sample.Metric["status"],
			}),
		}
		resource.Metrics = MetricForResource("instance", resource, aggregations)
		out = append(out, resource)
	}
	return out
}

func buildVolumes(samples []prom.Sample, aggregations []string) []*gnocchi.Resource {
	out := make([]*gnocchi.Resource, 0, len(samples))
	for _, sample := range samples {
		id := sample.Metric["id"]
		if id == "" {
			continue
		}
		resource := &gnocchi.Resource{
			ID:   id,
			Type: "volume",
			Attrs: mergeAttrs(commonResourceAttrs(id, sample.Timestamp, sample.Metric["tenant_id"], sample.Metric["user_id"]), map[string]any{
				"name":        sample.Metric["name"],
				"status":      sample.Metric["status"],
				"server_id":   sample.Metric["server_id"],
				"volume_type": sample.Metric["volume_type"],
				"bootable":    sample.Metric["bootable"],
			}),
		}
		resource.Metrics = MetricForResource("volume", resource, aggregations)
		out = append(out, resource)
	}
	return out
}

func buildNetworks(samples []prom.Sample, aggregations []string) []*gnocchi.Resource {
	out := make([]*gnocchi.Resource, 0, len(samples))
	for _, sample := range samples {
		id := sample.Metric["id"]
		if id == "" {
			continue
		}
		resource := &gnocchi.Resource{
			ID:   id,
			Type: "network",
			Attrs: mergeAttrs(commonResourceAttrs(id, sample.Timestamp, sample.Metric["tenant_id"], nil), map[string]any{
				"name":                      sample.Metric["name"],
				"status":                    sample.Metric["status"],
				"is_external":               sample.Metric["is_external"],
				"is_shared":                 sample.Metric["is_shared"],
				"provider_network_type":     sample.Metric["provider_network_type"],
				"provider_physical_network": sample.Metric["provider_physical_network"],
				"provider_segmentation_id":  sample.Metric["provider_segmentation_id"],
				"subnets":                   sample.Metric["subnets"],
				"tags":                      sample.Metric["tags"],
			}),
		}
		resource.Metrics = MetricForResource("network", resource, aggregations)
		out = append(out, resource)
	}
	return out
}

func buildPorts(samples []prom.Sample, networks []*gnocchi.Resource, aggregations []string) []*gnocchi.Resource {
	networkProjects := make(map[string]any, len(networks))
	for _, network := range networks {
		networkProjects[network.ID] = network.Attrs["project_id"]
	}

	out := make([]*gnocchi.Resource, 0, len(samples))
	for _, sample := range samples {
		id := sample.Metric["uuid"]
		if id == "" {
			continue
		}
		resource := &gnocchi.Resource{
			ID:   id,
			Type: "port",
			Attrs: mergeAttrs(commonResourceAttrs(id, sample.Timestamp, networkProjects[sample.Metric["network_id"]], nil), map[string]any{
				"project_id":       networkProjects[sample.Metric["network_id"]],
				"network_id":       sample.Metric["network_id"],
				"status":           sample.Metric["status"],
				"device_owner":     sample.Metric["device_owner"],
				"fixed_ips":        sample.Metric["fixed_ips"],
				"mac_address":      sample.Metric["mac_address"],
				"admin_state_up":   sample.Metric["admin_state_up"],
				"binding_vif_type": sample.Metric["binding_vif_type"],
			}),
		}
		resource.Metrics = MetricForResource("port", resource, aggregations)
		out = append(out, resource)
	}
	return out
}

func buildGeneric(resourcesByType map[string][]*gnocchi.Resource, aggregations []string) []*gnocchi.Resource {
	order := []string{"instance", "volume", "network", "port"}
	out := []*gnocchi.Resource{}
	for _, resourceType := range order {
		for _, resource := range resourcesByType[resourceType] {
			generic := &gnocchi.Resource{
				ID:   resource.ID,
				Type: "generic",
				Attrs: mergeAttrs(commonResourceAttrsFromExisting(resource), map[string]any{
					"resource_type": resource.Type,
				}),
				Metrics: map[string]*gnocchi.Metric{},
			}
			for name, metric := range resource.Metrics {
				copyMetric := *metric
				generic.Metrics[name] = &copyMetric
			}
			out = append(out, generic)
		}
	}
	return out
}

func commonResourceAttrs(id string, timestamp time.Time, projectID, userID any) map[string]any {
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	ts := timestamp.UTC().Format(time.RFC3339)
	return map[string]any{
		"project_id":           normalizeOptionalValue(projectID),
		"user_id":              normalizeOptionalValue(userID),
		"original_resource_id": id,
		"started_at":           ts,
		"ended_at":             nil,
		"revision_start":       ts,
		"revision_end":         nil,
	}
}

func commonResourceAttrsFromExisting(resource *gnocchi.Resource) map[string]any {
	return map[string]any{
		"project_id":           normalizeOptionalValue(resource.Attrs["project_id"]),
		"user_id":              normalizeOptionalValue(resource.Attrs["user_id"]),
		"original_resource_id": resource.ID,
		"started_at":           resource.Attrs["started_at"],
		"ended_at":             resource.Attrs["ended_at"],
		"revision_start":       resource.Attrs["revision_start"],
		"revision_end":         resource.Attrs["revision_end"],
	}
}

func mergeAttrs(base map[string]any, extras map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extras))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extras {
		merged[key] = value
	}
	return merged
}

func normalizeOptionalValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return v
	default:
		return value
	}
}
