package server

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/config"
)

func TestMeasureParamsFromRequestTreatsBareGranularityAsSeconds(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Default()}
	req := httptest.NewRequest("GET", "/v1/metric/test/measures?start=2024-01-01T00:00:00Z&stop=2024-01-01T00:10:00Z&granularity=300&aggregation=max", nil)

	params, err := s.measureParamsFromRequest(req)
	if err != nil {
		t.Fatalf("measureParamsFromRequest returned error: %v", err)
	}

	if params.Step != 5*time.Minute {
		t.Fatalf("expected 5m step, got %s", params.Step)
	}
	if params.Window != "300s" {
		t.Fatalf("expected Prometheus window 300s, got %q", params.Window)
	}
	if params.OutputGranularity != "300s" {
		t.Fatalf("expected output granularity 300s, got %q", params.OutputGranularity)
	}
}

func TestMeasureParamsFromRequestTreatsBareResampleAsSeconds(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Default()}
	req := httptest.NewRequest("GET", "/v1/metric/test/measures?start=2024-01-01T00:00:00Z&stop=2024-01-01T00:10:00Z&granularity=60s&resample=300&aggregation=mean", nil)

	params, err := s.measureParamsFromRequest(req)
	if err != nil {
		t.Fatalf("measureParamsFromRequest returned error: %v", err)
	}

	if params.Step != 5*time.Minute {
		t.Fatalf("expected 5m step from resample, got %s", params.Step)
	}
	if params.Window != "300s" {
		t.Fatalf("expected Prometheus window 300s from resample, got %q", params.Window)
	}
	if params.OutputGranularity != "300s" {
		t.Fatalf("expected output granularity 300s from resample, got %q", params.OutputGranularity)
	}
}
