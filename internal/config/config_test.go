package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndValidates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
server:
  address: ":9090"
keystone:
  auth_url: "https://keystone.example.com"
  username: "svc"
  password: "secret"
  project_name: "service"
prometheus:
  base_url: "https://prometheus.example.com"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.ReadTimeout == 0 {
		t.Fatalf("expected default read timeout")
	}
	if cfg.API.DefaultAggregation != "mean" {
		t.Fatalf("expected default aggregation, got %q", cfg.API.DefaultAggregation)
	}
	if cfg.Keystone.UserDomainName != "Default" {
		t.Fatalf("expected default user domain")
	}
}
