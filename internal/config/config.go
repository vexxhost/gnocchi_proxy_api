package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	defaultGranularities = []string{"60s", "300s", "3600s"}
	defaultAggregations  = []string{"mean", "min", "max", "sum", "last", "count"}
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Keystone   KeystoneConfig   `yaml:"keystone"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
	Catalog    CatalogConfig    `yaml:"catalog"`
	API        APIConfig        `yaml:"api"`
}

type ServerConfig struct {
	Address         string        `yaml:"address"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	IdleTimeout     time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

type KeystoneConfig struct {
	AuthURL            string        `yaml:"auth_url"`
	Username           string        `yaml:"username"`
	Password           string        `yaml:"password"`
	ProjectName        string        `yaml:"project_name"`
	UserDomainName     string        `yaml:"user_domain_name"`
	ProjectDomainName  string        `yaml:"project_domain_name"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify"`
	AdminRoles         []string      `yaml:"admin_roles"`
	ServiceTokenSkew   time.Duration `yaml:"service_token_skew"`
}

type PrometheusConfig struct {
	BaseURL          string            `yaml:"base_url"`
	QueryTimeout     time.Duration     `yaml:"query_timeout"`
	Headers          map[string]string `yaml:"headers"`
	LibvirtSelector  string            `yaml:"libvirt_selector"`
	DatabaseSelector string            `yaml:"database_selector"`
}

type CatalogConfig struct {
	RefreshInterval time.Duration `yaml:"refresh_interval"`
}

type APIConfig struct {
	SupportedGranularities []string `yaml:"supported_granularities"`
	SupportedAggregations  []string `yaml:"supported_aggregations"`
	DefaultGranularity     string   `yaml:"default_granularity"`
	DefaultAggregation     string   `yaml:"default_aggregation"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Address:         ":8080",
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    15 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 10 * time.Second,
		},
		Keystone: KeystoneConfig{
			UserDomainName:    "Default",
			ProjectDomainName: "Default",
			AdminRoles:        []string{"admin"},
			ServiceTokenSkew:  30 * time.Second,
		},
		Prometheus: PrometheusConfig{
			QueryTimeout: 30 * time.Second,
			Headers:      map[string]string{},
		},
		Catalog: CatalogConfig{
			RefreshInterval: 60 * time.Second,
		},
		API: APIConfig{
			SupportedGranularities: append([]string(nil), defaultGranularities...),
			SupportedAggregations:  append([]string(nil), defaultAggregations...),
			DefaultGranularity:     "60s",
			DefaultAggregation:     "mean",
		},
	}
}

func (c *Config) Validate() error {
	var problems []error

	if c.Server.Address == "" {
		problems = append(problems, errors.New("server.address is required"))
	}
	if c.Prometheus.BaseURL == "" {
		problems = append(problems, errors.New("prometheus.base_url is required"))
	}
	if c.Keystone.AuthURL == "" {
		problems = append(problems, errors.New("keystone.auth_url is required"))
	}
	if c.Keystone.Username == "" {
		problems = append(problems, errors.New("keystone.username is required"))
	}
	if c.Keystone.Password == "" {
		problems = append(problems, errors.New("keystone.password is required"))
	}
	if c.Keystone.ProjectName == "" {
		problems = append(problems, errors.New("keystone.project_name is required"))
	}
	if len(c.API.SupportedGranularities) == 0 {
		problems = append(problems, errors.New("api.supported_granularities must not be empty"))
	}
	if len(c.API.SupportedAggregations) == 0 {
		problems = append(problems, errors.New("api.supported_aggregations must not be empty"))
	}
	if !slices.Contains(c.API.SupportedGranularities, c.API.DefaultGranularity) {
		problems = append(problems, fmt.Errorf("api.default_granularity %q is not in api.supported_granularities", c.API.DefaultGranularity))
	}
	if !slices.Contains(c.API.SupportedAggregations, c.API.DefaultAggregation) {
		problems = append(problems, fmt.Errorf("api.default_aggregation %q is not in api.supported_aggregations", c.API.DefaultAggregation))
	}

	return errors.Join(problems...)
}
