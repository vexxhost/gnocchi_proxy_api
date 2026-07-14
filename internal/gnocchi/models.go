package gnocchi

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	metricNamespace   = uuid.MustParse("1e27aead-d607-4e7d-a073-b4442c61f7f5")
	resourceNamespace = uuid.MustParse("6c808097-0543-4b0e-8818-76fa5a4d90f8")
)

type Context struct {
	Token     string
	UserID    string
	ProjectID string
	Roles     []string
	IsAdmin   bool
}

type ArchivePolicy struct {
	Name               string                    `json:"name"`
	AggregationMethods []string                  `json:"aggregation_methods"`
	BackWindow         int                       `json:"back_window"`
	Definition         []ArchivePolicyDefinition `json:"definition"`
}

type ArchivePolicyDefinition struct {
	Granularity string `json:"granularity"`
	Points      int    `json:"points"`
	Timespan    string `json:"timespan"`
}

type ResourceType struct {
	Name       string              `json:"name"`
	Attributes []ResourceTypeField `json:"attributes"`
	State      string              `json:"state"`
}

type ResourceTypeField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
}

type Resource struct {
	ID      string             `json:"id"`
	Type    string             `json:"type"`
	Attrs   map[string]any     `json:"-"`
	Metrics map[string]*Metric `json:"metrics,omitempty"`
}

type Metric struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	ArchivePolicyName  string   `json:"archive_policy_name"`
	ResourceID         string   `json:"resource_id"`
	ResourceType       string   `json:"resource_type"`
	Unit               string   `json:"unit"`
	AggregationMethods []string `json:"aggregation_methods"`
}

type Measure struct {
	Timestamp   time.Time
	Granularity string
	Value       float64
}

func MetricID(resourceType, resourceID, metricName string) string {
	return uuid.NewSHA1(metricNamespace, []byte(resourceType+"/"+resourceID+"/"+metricName)).String()
}

// ResourceID returns a stable UUID for resource types whose original IDs are
// composite values, such as a Nova instance ID plus a libvirt device name.
func ResourceID(resourceType, originalResourceID string) string {
	return uuid.NewSHA1(resourceNamespace, []byte(resourceType+"/"+originalResourceID)).String()
}

func (r *Resource) Clone() *Resource {
	clone := &Resource{
		ID:      r.ID,
		Type:    r.Type,
		Attrs:   make(map[string]any, len(r.Attrs)),
		Metrics: make(map[string]*Metric, len(r.Metrics)),
	}
	for k, v := range r.Attrs {
		clone.Attrs[k] = v
	}
	for k, v := range r.Metrics {
		metric := *v
		clone.Metrics[k] = &metric
	}
	return clone
}

func (r *Resource) ToResponse(attrs []string) map[string]any {
	out := map[string]any{
		"id": r.ID,
	}
	if len(attrs) == 0 {
		out["type"] = r.Type
		keys := make([]string, 0, len(r.Attrs))
		for key := range r.Attrs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out[key] = r.Attrs[key]
		}
		out["metrics"] = metricIDs(r.Metrics)
		return out
	}

	for _, attr := range attrs {
		if attr == "metrics" {
			out["metrics"] = metricIDs(r.Metrics)
			continue
		}
		if value, ok := r.Attrs[attr]; ok {
			out[attr] = value
		}
	}
	return out
}

func metricIDs(metrics map[string]*Metric) map[string]string {
	out := make(map[string]string, len(metrics))
	for name, metric := range metrics {
		out[name] = metric.ID
	}
	return out
}

// MeasuresResponse returns Gnocchi measure triples. RFC 3339 is the
// interoperable default. naive_utc exists only for legacy callers which append
// a UTC designator themselves before parsing a timestamp.
func MeasuresResponse(measures []Measure, timestampFormat string) [][]any {
	out := make([][]any, 0, len(measures))
	for _, measure := range measures {
		out = append(out, []any{
			formatMeasureTimestamp(measure.Timestamp, timestampFormat),
			granularityValue(measure.Granularity),
			measure.Value,
		})
	}
	return out
}

func formatMeasureTimestamp(timestamp time.Time, timestampFormat string) string {
	timestamp = timestamp.UTC()
	if timestampFormat == "naive_utc" {
		return timestamp.Format("2006-01-02T15:04:05")
	}
	return timestamp.Format(time.RFC3339)
}

func granularityValue(value string) any {
	if value == "" {
		return nil
	}
	if _, err := time.ParseDuration(value); err == nil {
		duration, _ := time.ParseDuration(value)
		return duration.Seconds()
	}
	return value
}

func ParseDurationOrCalendar(raw string) (time.Duration, bool, error) {
	if raw == "" {
		return 0, false, nil
	}
	if strings.HasSuffix(raw, "s") || strings.HasSuffix(raw, "m") || strings.HasSuffix(raw, "h") {
		duration, err := time.ParseDuration(raw)
		return duration, false, err
	}
	return 0, true, nil
}

func ParseFloat(v any) (float64, bool) {
	switch value := v.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case string:
		f, err := strconv.ParseFloat(value, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func MustResourceType(name string, attrs ...ResourceTypeField) ResourceType {
	return ResourceType{
		Name:       name,
		Attributes: attrs,
		State:      "active",
	}
}

func GranularityToString(d time.Duration) string {
	if d%(time.Hour) == 0 {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return d.String()
}
