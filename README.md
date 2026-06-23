# Gnocchi Proxy API

`gnocchi-proxy-api` is a read-only, Gnocchi-compatible HTTP service written in Go. It validates Keystone tokens directly, translates supported Gnocchi API calls into Prometheus queries, and serves OpenStack-shaped resource and metric data without deploying a real Gnocchi time-series backend.

The service is designed for Kubernetes-based OpenStack environments where Prometheus already scrapes:

- [`prometheus-libvirt-exporter`](https://github.com/inovex/prometheus-libvirt-exporter) for instance telemetry
- [`openstack_database_exporter`](https://github.com/vexxhost/openstack_database_exporter) for Nova, Cinder, and Neutron metadata/state

## Architecture

1. A client sends a Gnocchi-style request with `X-Auth-Token`.
2. `gnocchi-proxy-api` validates the token against Keystone with a cached service token.
3. The proxy refreshes an in-memory resource catalog from Prometheus instant queries.
4. The proxy filters resources by `project_id` unless the caller has an admin role.
5. Supported metrics and aggregates are translated into PromQL and returned in Gnocchi-compatible JSON.

## Repository Layout

```text
cmd/gnocchi-proxy-api       Main entrypoint
internal/catalog            Resource catalog refresh and filter logic
internal/config             YAML config loading and validation
internal/gnocchi            Gnocchi response models and helpers
internal/keystone           Keystone v3 auth and token validation
internal/prom               Prometheus HTTP client
internal/server             HTTP routes, measures, and aggregate evaluation
tests/api                   End-to-end API tests with mocked Keystone/Prometheus
charts/gnocchi-proxy-api    Standalone Helm chart
```

## Configuration

Use a single YAML config file. Start from [config.example.yaml](/Users/yaguang.tang/Documents/gnocchi-proxy-api/config.example.yaml).

Top-level sections:

- `server`: listen address and HTTP timeouts
- `keystone`: Keystone v3 auth endpoint, service-user credentials, admin roles, TLS behavior
- `prometheus`: Prometheus URL, timeout, optional extra headers, label selectors for libvirt and database exporters
- `catalog`: in-memory catalog refresh interval
- `api`: supported granularities and aggregation methods exposed to clients

Run locally:

```bash
go run ./cmd/gnocchi-proxy-api -config config.example.yaml
```

## Supported API Surface

| Surface | v1 status | Notes |
| --- | --- | --- |
| `GET /`, `GET /v1/capabilities`, `GET /v1/status` | Supported | Discovery and synthetic status payloads |
| `GET /v1/archive_policy`, `GET /v1/archive_policy/{name}` | Supported | Read-only synthetic `prometheus` archive policy |
| `GET /v1/resource_type`, `GET /v1/resource_type/{name}` | Supported | `instance`, `volume`, `network`, `port`, `generic` |
| `GET /v1/resource/{type}`, `GET /v1/resource/{type}/{id}` | Supported | Supports `limit`, `marker`, `sort`, `attrs` |
| `POST /v1/search/resource/{type}` | Supported | Supports JSON filters and simple `filter=` expressions |
| `GET /v1/metric`, `GET /v1/metric/{id}` | Supported | Synthetic read-only metric catalog |
| `GET /v1/resource/{type}/{id}/metric/{name}` | Supported | Metric lookup by resource and name |
| `GET /v1/metric/{id}/measures`, `GET /v1/resource/{type}/{id}/metric/{name}/measures` | Supported | `start`, `stop`, `granularity`, `aggregation`, `resample`, `refresh` |
| `POST /v1/aggregates` | Supported | Read-only aggregate expressions over supported metrics |
| `history=true` resource queries | Not supported | No revision/history store |
| `POST/PATCH/DELETE /v1/resource*` | Not supported | Read-only facade |
| `POST/DELETE /v1/metric*`, `POST /v1/*/measures`, `POST /v1/batch/*` | Not supported | No ingestion or mutable catalog |
| `POST /v1/search/metric` | Not supported | Out of scope in v1 |
| `POST/PATCH/DELETE /v1/archive_policy*`, `GET/POST /v1/archive_policy_rule*` | Not supported | Synthetic archive policy only |
| Resource types beyond `instance`, `volume`, `network`, `port`, `generic` | Not supported | No source coverage in v1 |

## Resource and Metric Coverage

| Resource type | v1 metric families | Source |
| --- | --- | --- |
| `instance` | `cpu.time`, `vcpus`, `memory.*`, `disk.read.bytes`, `disk.write.bytes`, `disk.capacity`, `network.incoming.bytes`, `network.outgoing.bytes`, `status`, `local_gb` | libvirt exporter + Nova collector |
| `volume` | `volume.size`, `volume.status` | Cinder volume collector |
| `network` | `network.present`, `network.status` plus provider/shared/external attrs | Neutron network collector |
| `port` | `port.present`, `port.status` plus `network_id`, `device_owner`, `fixed_ips`, `mac_address` attrs | Neutron port collector |

## Known Gaps

| Missing area | Why it is not implemented in v1 |
| --- | --- |
| Per-volume read/write I/O measures | Current exporters do not expose stable per-volume throughput series keyed by Cinder volume ID |
| Per-Neutron-port rx/tx/error measures | Current exporters do not expose stable traffic series keyed by real Neutron port UUID |
| Historical resource revisions | The proxy intentionally keeps only an in-memory current-state catalog |

## Local Validation

```bash
go test ./...
go build ./cmd/gnocchi-proxy-api
helm lint charts/gnocchi-proxy-api
helm template gnocchi-proxy-api charts/gnocchi-proxy-api
docker build -t ghcr.io/<your-org>/gnocchi-proxy-api:dev .
```

The service exposes:

- `/healthz` for liveness
- `/readyz` for readiness
- `/metrics` for Prometheus scraping

## OpenStack Registration and Kubernetes Deployment

### 1. Create the service user

```bash
openstack user create --domain Default --password '<PASSWORD>' gnocchi-proxy
openstack role add --project service --user gnocchi-proxy admin
```

### 2. Create the `metric` service entry

```bash
openstack service create --name gnocchi --description 'Gnocchi-compatible Prometheus proxy' metric
```

### 3. Create Keystone endpoints for the proxy

Replace the URLs with the address that will front the Kubernetes Service or Ingress:

```bash
openstack endpoint create --region RegionOne metric public   https://gnocchi.example.com/v1
openstack endpoint create --region RegionOne metric internal http://gnocchi-proxy-api.openstack.svc.cluster.local:8080/v1
openstack endpoint create --region RegionOne metric admin    http://gnocchi-proxy-api.openstack.svc.cluster.local:8080/v1
```

### 4. Prepare Helm values

Copy [config.example.yaml](/Users/yaguang.tang/Documents/gnocchi-proxy-api/config.example.yaml) into your own values file and update:

- `config.keystone.auth_url`
- `config.keystone.username`
- `config.keystone.password`
- `config.prometheus.base_url`
- `config.prometheus.libvirt_selector`
- `config.prometheus.database_selector`
- `image.repository`
- `image.tag`

### 5. Deploy with Helm

```bash
helm upgrade --install gnocchi-proxy-api ./charts/gnocchi-proxy-api \
  --namespace openstack \
  --create-namespace \
  -f values.yaml
```

### 6. Verify the deployment

```bash
kubectl get pods -n openstack -l app.kubernetes.io/name=gnocchi-proxy-api
kubectl port-forward -n openstack svc/gnocchi-proxy-api-gnocchi-proxy-api 8080:8080
curl -s http://127.0.0.1:8080/healthz
```

## CI and Release

- `.github/workflows/ci.yml` runs `go test`, `go build`, `helm lint`, `helm template`, and `docker build`.
- `.github/workflows/release.yml` builds tagged binaries, publishes release archives, and pushes multi-arch images to GHCR.
