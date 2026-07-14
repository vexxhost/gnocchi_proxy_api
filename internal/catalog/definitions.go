package catalog

import (
	"fmt"
	"strings"
	"time"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/gnocchi"
)

type QueryMode string

const (
	QueryModeRangeFunction QueryMode = "range_function"
	QueryModeLabelStatus   QueryMode = "label_status"
	QueryModePresenceCount QueryMode = "presence_count"
)

type MetricDefinition struct {
	ResourceType string
	Name         string
	Unit         string
	Mode         QueryMode
	StatusMap    map[string]float64
	SelectorKey  string
	ValueQuery   func(id string, selectors Selectors, aggregation string, window string) string
	// ResourceValueQuery is used when a metric needs attributes from the
	// resource, rather than only its ID. Device resources have synthetic UUIDs,
	// so their PromQL needs the instance ID and libvirt device name instead.
	ResourceValueQuery func(resource *gnocchi.Resource, selectors Selectors, aggregation string, window string) string
	RawQuery           func(id string, selectors Selectors) string
}

type Selectors struct {
	Libvirt  string
	Database string
}

var metricDefinitions = map[string][]MetricDefinition{
	"instance": {
		buildInstanceMetric("cpu.time", "s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_info_cpu_time_seconds_total", id, selectors))
		}),
		buildInstanceMetricAlias("cpu", "s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_info_cpu_time_seconds_total", id, selectors))
		}),
		buildInstanceMetric("cpu_util", "%", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, cpuUtilExpr(id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetric("vcpus", "count", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_vcpu_current", id, selectors))
		}),
		buildInstanceMetric("memory.usage", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, memoryUsageExpr(id, selectors))
		}),
		buildInstanceMetric("memory.maximum", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_info_maximum_memory_bytes", id, selectors))
		}),
		buildInstanceMetricAlias("memory", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_info_maximum_memory_bytes", id, selectors))
		}),
		buildInstanceMetric("memory.available", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_memory_stats_available_bytes", id, selectors))
		}),
		buildInstanceMetric("memory.usable", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_memory_stats_usable_bytes", id, selectors))
		}),
		buildInstanceMetric("memory.rss", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_memory_stats_rss_bytes", id, selectors))
		}),
		buildInstanceMetricAlias("memory.resident", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_memory_stats_rss_bytes", id, selectors))
		}),
		buildInstanceMetric("memory.used_percent", "%", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_memory_stats_used_percent", id, selectors))
		}),
		buildInstanceMetric("disk.read.bytes", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_read_bytes_total", id, selectors))
		}),
		buildInstanceMetricAlias("disk.device.read.bytes", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_read_bytes_total", id, selectors))
		}),
		buildInstanceMetric("disk.read.bytes.rate", "B/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtRateJoined("libvirt_domain_block_stats_read_bytes_total", id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetricAlias("disk.device.read.bytes.rate", "B/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtRateJoined("libvirt_domain_block_stats_read_bytes_total", id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetric("disk.write.bytes", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_write_bytes_total", id, selectors))
		}),
		buildInstanceMetricAlias("disk.device.write.bytes", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_write_bytes_total", id, selectors))
		}),
		buildInstanceMetric("disk.write.bytes.rate", "B/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtRateJoined("libvirt_domain_block_stats_write_bytes_total", id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetricAlias("disk.device.write.bytes.rate", "B/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtRateJoined("libvirt_domain_block_stats_write_bytes_total", id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetric("disk.read.requests", "request", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_read_requests_total", id, selectors))
		}),
		buildInstanceMetricAlias("disk.device.read.requests", "request", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_read_requests_total", id, selectors))
		}),
		buildInstanceMetric("disk.read.requests.rate", "request/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtRateJoined("libvirt_domain_block_stats_read_requests_total", id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetricAlias("disk.device.read.requests.rate", "request/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtRateJoined("libvirt_domain_block_stats_read_requests_total", id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetric("disk.write.requests", "request", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_write_requests_total", id, selectors))
		}),
		buildInstanceMetricAlias("disk.device.write.requests", "request", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_write_requests_total", id, selectors))
		}),
		buildInstanceMetric("disk.write.requests.rate", "request/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtRateJoined("libvirt_domain_block_stats_write_requests_total", id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetricAlias("disk.device.write.requests.rate", "request/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtRateJoined("libvirt_domain_block_stats_write_requests_total", id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetric("disk.iops", "count/s", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, diskIOPSExpr(id, selectors, minRateLookback(window)))
		}),
		buildInstanceMetric("disk.capacity", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_capacity_bytes", id, selectors))
		}),
		buildInstanceMetricAlias("disk.device.capacity", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_block_stats_capacity_bytes", id, selectors))
		}),
		buildInstanceMetric("network.incoming.bytes", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_interface_stats_receive_bytes_total", id, selectors))
		}),
		buildInstanceMetric("network.outgoing.bytes", "By", func(id string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtJoined("libvirt_domain_interface_stats_transmit_bytes_total", id, selectors))
		}),
		{
			ResourceType: "instance",
			Name:         "status",
			Unit:         "state",
			Mode:         QueryModeRangeFunction,
			ValueQuery: func(id string, selectors Selectors, aggregation string, window string) string {
				return rangeWrapped(aggregation, window, metricSelector("openstack_nova_server_status", selectors.Database, "id", id))
			},
		},
		{
			ResourceType: "instance",
			Name:         "local_gb",
			Unit:         "GB",
			Mode:         QueryModeRangeFunction,
			ValueQuery: func(id string, selectors Selectors, aggregation string, window string) string {
				return rangeWrapped(aggregation, window, metricSelector("openstack_nova_server_local_gb", selectors.Database, "id", id))
			},
		},
	},
	"instance_network_interface": {
		buildInstanceNetworkInterfaceMetric("network.incoming.bytes", "By", func(instanceID, targetDevice string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtDeviceJoined("libvirt_domain_interface_stats_receive_bytes_total", instanceID, targetDevice, selectors))
		}),
		buildInstanceNetworkInterfaceMetric("network.incoming.bytes.rate", "B/s", func(instanceID, targetDevice string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtDeviceRateJoined("libvirt_domain_interface_stats_receive_bytes_total", instanceID, targetDevice, selectors, minRateLookback(window)))
		}),
		buildInstanceNetworkInterfaceMetric("network.outgoing.bytes", "By", func(instanceID, targetDevice string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtDeviceJoined("libvirt_domain_interface_stats_transmit_bytes_total", instanceID, targetDevice, selectors))
		}),
		buildInstanceNetworkInterfaceMetric("network.outgoing.bytes.rate", "B/s", func(instanceID, targetDevice string, selectors Selectors, aggregation string, window string) string {
			return rangeWrapped(aggregation, window, libvirtDeviceRateJoined("libvirt_domain_interface_stats_transmit_bytes_total", instanceID, targetDevice, selectors, minRateLookback(window)))
		}),
	},
	"volume": {
		{
			ResourceType: "volume",
			Name:         "volume.size",
			Unit:         "GB",
			Mode:         QueryModeRangeFunction,
			ValueQuery: func(id string, selectors Selectors, aggregation string, window string) string {
				return rangeWrapped(aggregation, window, metricSelector("openstack_cinder_volume_gb", selectors.Database, "id", id))
			},
		},
		{
			ResourceType: "volume",
			Name:         "volume.status",
			Unit:         "state",
			Mode:         QueryModeRangeFunction,
			ValueQuery: func(id string, selectors Selectors, aggregation string, window string) string {
				return rangeWrapped(aggregation, window, metricSelector("openstack_cinder_volume_status", selectors.Database, "id", id))
			},
		},
	},
	"network": {
		{
			ResourceType: "network",
			Name:         "network.present",
			Unit:         "count",
			Mode:         QueryModePresenceCount,
			RawQuery: func(id string, selectors Selectors) string {
				return metricSelector("openstack_neutron_network", selectors.Database, "id", id)
			},
		},
		{
			ResourceType: "network",
			Name:         "network.status",
			Unit:         "state",
			Mode:         QueryModeLabelStatus,
			StatusMap: map[string]float64{
				"ACTIVE":  1,
				"DOWN":    2,
				"BUILD":   3,
				"ERROR":   4,
				"UNKNOWN": 0,
			},
			SelectorKey: "status",
			RawQuery: func(id string, selectors Selectors) string {
				return metricSelector("openstack_neutron_network", selectors.Database, "id", id)
			},
		},
	},
	"port": {
		{
			ResourceType: "port",
			Name:         "port.present",
			Unit:         "count",
			Mode:         QueryModePresenceCount,
			RawQuery: func(id string, selectors Selectors) string {
				return metricSelector("openstack_neutron_port", selectors.Database, "uuid", id)
			},
		},
		{
			ResourceType: "port",
			Name:         "port.status",
			Unit:         "state",
			Mode:         QueryModeLabelStatus,
			StatusMap: map[string]float64{
				"ACTIVE":  1,
				"DOWN":    2,
				"BUILD":   3,
				"ERROR":   4,
				"N/A":     0,
				"UNKNOWN": 0,
			},
			SelectorKey: "status",
			RawQuery: func(id string, selectors Selectors) string {
				return metricSelector("openstack_neutron_port", selectors.Database, "uuid", id)
			},
		},
	},
}

func Definitions(resourceType string) []MetricDefinition {
	return append([]MetricDefinition(nil), metricDefinitions[resourceType]...)
}

func DefinitionByName(resourceType, metricName string) (MetricDefinition, bool) {
	for _, definition := range metricDefinitions[resourceType] {
		if definition.Name == metricName {
			return definition, true
		}
	}
	return MetricDefinition{}, false
}

func AllDefinitions() map[string][]MetricDefinition {
	out := make(map[string][]MetricDefinition, len(metricDefinitions))
	for key := range metricDefinitions {
		out[key] = Definitions(key)
	}
	return out
}

func buildInstanceMetric(name, unit string, query func(id string, selectors Selectors, aggregation string, window string) string) MetricDefinition {
	return MetricDefinition{
		ResourceType: "instance",
		Name:         name,
		Unit:         unit,
		Mode:         QueryModeRangeFunction,
		ValueQuery:   query,
	}
}

func buildInstanceMetricAlias(name, unit string, query func(id string, selectors Selectors, aggregation string, window string) string) MetricDefinition {
	return buildInstanceMetric(name, unit, query)
}

func buildInstanceNetworkInterfaceMetric(name, unit string, query func(instanceID, targetDevice string, selectors Selectors, aggregation string, window string) string) MetricDefinition {
	return MetricDefinition{
		ResourceType: "instance_network_interface",
		Name:         name,
		Unit:         unit,
		Mode:         QueryModeRangeFunction,
		ResourceValueQuery: func(resource *gnocchi.Resource, selectors Selectors, aggregation string, window string) string {
			instanceID, _ := resource.Attrs["instance_id"].(string)
			targetDevice, _ := resource.Attrs["name"].(string)
			if instanceID == "" || targetDevice == "" {
				return ""
			}
			return query(instanceID, targetDevice, selectors, aggregation, window)
		},
	}
}

func rangeWrapped(aggregation, window, expr string) string {
	function := map[string]string{
		"mean":  "avg_over_time",
		"min":   "min_over_time",
		"max":   "max_over_time",
		"sum":   "sum_over_time",
		"last":  "last_over_time",
		"count": "count_over_time",
	}[strings.ToLower(aggregation)]
	if function == "" {
		function = "avg_over_time"
	}
	return fmt.Sprintf("%s((%s)[%s:%s])", function, expr, window, window)
}

func metricSelector(metric, selector, label, value string) string {
	parts := make([]string, 0, 2)
	if selector != "" {
		parts = append(parts, strings.TrimSpace(selector))
	}
	if label != "" {
		parts = append(parts, fmt.Sprintf(`%s=%q`, label, value))
	}
	return fmt.Sprintf("%s{%s}", metric, strings.Join(parts, ","))
}

func libvirtJoined(metric, instanceID string, selectors Selectors) string {
	left := metricSelector(metric, selectors.Libvirt, "", "")
	left = strings.Replace(left, `{}`, "", 1)
	right := metricSelector("libvirt_domain_openstack_info", selectors.Libvirt, "instance_id", instanceID)
	return fmt.Sprintf("sum((%s) * on(domain) group_left(instance_id) %s)", left, right)
}

func libvirtRateJoined(metric, instanceID string, selectors Selectors, window string) string {
	left := metricSelector(metric, selectors.Libvirt, "", "")
	left = strings.Replace(left, `{}`, "", 1)
	right := metricSelector("libvirt_domain_openstack_info", selectors.Libvirt, "instance_id", instanceID)
	return fmt.Sprintf("sum((rate(%s[%s])) * on(domain) group_left(instance_id) %s)", left, window, right)
}

func libvirtDeviceJoined(metric, instanceID, targetDevice string, selectors Selectors) string {
	left := metricSelector(metric, selectors.Libvirt, "target_device", targetDevice)
	right := metricSelector("libvirt_domain_openstack_info", selectors.Libvirt, "instance_id", instanceID)
	return fmt.Sprintf("sum((%s) * on(domain) group_left(instance_id) %s)", left, right)
}

func libvirtDeviceRateJoined(metric, instanceID, targetDevice string, selectors Selectors, window string) string {
	left := metricSelector(metric, selectors.Libvirt, "target_device", targetDevice)
	right := metricSelector("libvirt_domain_openstack_info", selectors.Libvirt, "instance_id", instanceID)
	return fmt.Sprintf("sum((rate(%s[%s])) * on(domain) group_left(instance_id) %s)", left, window, right)
}

func cpuUtilExpr(instanceID string, selectors Selectors, window string) string {
	cpuTimeRate := libvirtRateJoined("libvirt_domain_info_cpu_time_seconds_total", instanceID, selectors, window)
	vcpus := libvirtJoined("libvirt_domain_vcpu_current", instanceID, selectors)
	return fmt.Sprintf("(100 * (%s)) / clamp_min((%s), 1)", cpuTimeRate, vcpus)
}

func memoryUsageExpr(instanceID string, selectors Selectors) string {
	available := libvirtJoined("libvirt_domain_memory_stats_available_bytes", instanceID, selectors)
	usable := libvirtJoined("libvirt_domain_memory_stats_usable_bytes", instanceID, selectors)
	return fmt.Sprintf("clamp_min((%s) - (%s), 0)", available, usable)
}

// diskIOPSExpr is the Gnocchi-compatible instance-level disk.iops metric.
// libvirt exposes cumulative read and write request counters, so the total
// IOPS is the sum of their per-second rates across all disks on the instance.
func diskIOPSExpr(instanceID string, selectors Selectors, window string) string {
	reads := libvirtRateJoined("libvirt_domain_block_stats_read_requests_total", instanceID, selectors, window)
	writes := libvirtRateJoined("libvirt_domain_block_stats_write_requests_total", instanceID, selectors, window)
	return fmt.Sprintf("(%s) + (%s)", reads, writes)
}

func minRateLookback(window string) string {
	duration, err := time.ParseDuration(window)
	if err != nil || duration < 2*time.Minute {
		return "120s"
	}
	return window
}

func MetricForResource(resourceType string, resource *gnocchi.Resource, supportedAggregations []string) map[string]*gnocchi.Metric {
	defs := Definitions(resourceType)
	metrics := make(map[string]*gnocchi.Metric, len(defs))
	for _, def := range defs {
		metrics[def.Name] = &gnocchi.Metric{
			ID:                 gnocchi.MetricID(resourceType, resource.ID, def.Name),
			Name:               def.Name,
			ArchivePolicyName:  "prometheus",
			ResourceID:         resource.ID,
			ResourceType:       resourceType,
			Unit:               def.Unit,
			AggregationMethods: append([]string(nil), supportedAggregations...),
		}
	}
	return metrics
}
