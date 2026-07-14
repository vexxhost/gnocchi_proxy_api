package gnocchi

import (
	"testing"
	"time"
)

func TestMetricIDIsDeterministic(t *testing.T) {
	t.Parallel()

	first := MetricID("instance", "instance-a", "cpu.time")
	second := MetricID("instance", "instance-a", "cpu.time")
	third := MetricID("instance", "instance-b", "cpu.time")

	if first != second {
		t.Fatalf("expected deterministic metric ID, got %q and %q", first, second)
	}
	if first == third {
		t.Fatalf("expected different resources to produce different metric IDs")
	}
}

func TestResourceIDIsDeterministic(t *testing.T) {
	t.Parallel()

	first := ResourceID("instance_disk", "instance-a-vda")
	second := ResourceID("instance_disk", "instance-a-vda")
	third := ResourceID("instance_network_interface", "instance-a-vda")

	if first != second {
		t.Fatalf("expected deterministic resource ID, got %q and %q", first, second)
	}
	if first == third {
		t.Fatalf("expected resource type to contribute to resource ID")
	}
}

func TestMeasuresResponseTimestampFormats(t *testing.T) {
	timestamp := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	measures := []Measure{{Timestamp: timestamp, Granularity: "300s", Value: 42}}

	if got := MeasuresResponse(measures, "rfc3339")[0][0]; got != "2026-07-13T12:00:00Z" {
		t.Fatalf("expected RFC 3339 timestamp, got %#v", got)
	}
	if got := MeasuresResponse(measures, "naive_utc")[0][0]; got != "2026-07-13T12:00:00" {
		t.Fatalf("expected timezone-free UTC timestamp for legacy caller, got %#v", got)
	}
}
