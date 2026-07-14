package gnocchi

import "testing"

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
