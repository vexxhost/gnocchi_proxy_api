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

Measure response timestamps default to RFC 3339 UTC (for example, `2026-07-13T12:00:00Z`). A legacy caller that incorrectly appends `Z` before parsing can use `api.measure_timestamp_format: naive_utc`; it receives `2026-07-13T12:00:00`, which it can safely turn into UTC. Keep the default for normal Gnocchi-compatible clients.

Run locally:

```bash
go run ./cmd/gnocchi-proxy-api -config config.example.yaml
```

## Supported API Surface

| Surface | v1 status | Notes |
| --- | --- | --- |
| `GET /`, `GET /v1/capabilities`, `GET /v1/status` | Supported | Discovery and synthetic status payloads |
| `GET /v1/archive_policy`, `GET /v1/archive_policy/{name}` | Supported | Read-only synthetic `prometheus` archive policy |
| `GET /v1/resource_type`, `GET /v1/resource_type/{name}` | Supported | `instance`, `instance_network_interface`, `instance_disk`, `volume`, `network`, `port`, `generic` |
| `GET /v1/resource/{type}`, `GET /v1/resource/{type}/{id}` | Supported | Supports `limit`, `marker`, `sort`, `attrs`; includes common Gnocchi resource fields such as `project_id`, `user_id`, `original_resource_id`, `started_at`, and revision timestamps |
| `POST /v1/search/resource/{type}` | Supported | Supports JSON filters and simple `filter=` expressions |
| `GET /v1/metric`, `GET /v1/metric/{id}` | Supported | Synthetic read-only metric catalog |
| `GET /v1/resource/{type}/{id}/metric`, `GET /v1/resource/{type}/{id}/metric/{name}` | Supported | Resource-scoped metric list and metric lookup by resource and name |
| `GET /v1/metric/{id}/measures`, `GET /v1/resource/{type}/{id}/metric/{name}/measures` | Supported | `start`, `stop`, `granularity`, `aggregation`, `resample`, `refresh`; bare numeric `granularity` and `resample` values are treated as seconds like Gnocchi |
| `POST /v1/aggregates` | Supported | Read-only aggregate expressions over supported metrics |
| `history=true` resource queries | Not supported | No revision/history store |
| `POST/PATCH/DELETE /v1/resource*` | Not supported | Read-only facade |
| `POST/DELETE /v1/metric*`, `POST /v1/*/measures`, `POST /v1/batch/*` | Not supported | No ingestion or mutable catalog |
| `POST /v1/search/metric` | Not supported | Out of scope in v1 |
| `POST/PATCH/DELETE /v1/archive_policy*`, `GET/POST /v1/archive_policy_rule*` | Not supported | Synthetic archive policy only |
| Resource types beyond `instance`, `instance_network_interface`, `instance_disk`, `volume`, `network`, `port`, `generic` | Not supported | No source coverage in v1 |

## Resource and Metric Coverage

| Resource type | v1 metric families | Source |
| --- | --- | --- |
| `instance` | `cpu.time` (`cpu` alias), `cpu_util`, `vcpus`, `memory.*` (`memory` and `memory.resident` aliases included), disk read/write byte and request counters plus their rates (`disk.device.*` aliases included), `disk.capacity`, `network.incoming.bytes`, `network.outgoing.bytes`, `status`, `local_gb` | libvirt exporter + Nova collector |
| `instance_network_interface` | Resource discovery only | libvirt interface counters joined with `libvirt_domain_openstack_info` |
| `instance_disk` | Resource discovery only | libvirt block counters joined with `libvirt_domain_openstack_info` |
| `volume` | `volume.size`, `volume.status` | Cinder volume collector |
| `network` | `network.present`, `network.status` plus provider/shared/external attrs | Neutron network collector |
| `port` | `port.present`, `port.status` plus `network_id`, `device_owner`, `fixed_ips`, `mac_address` attrs | Neutron port collector |

## Ceilometer/Gnocchi Compatibility Checklist

This table compares the proxy metric names above with the metric names OpenStack users are likely to query through Ceilometer or Gnocchi. Where the names differ, the proxy now exposes the Gnocchi-compatible name as an alias while keeping the original proxy-native name available.

| Resource type | Proxy metric name | Ceilometer/Gnocchi query name | Supported by proxy today | Compatibility note |
| --- | --- | --- | --- | --- |
| `instance` | `cpu.time` | `cpu` | Yes | `cpu` is a compatibility alias over the same CPU time series. |
| `instance` | `cpu_util` | `cpu_util` | Yes | Legacy compatibility metric supported explicitly by the proxy. |
| `instance` | `vcpus` | `vcpus` | Yes | Direct name match. |
| `instance` | `memory.usage` | `memory.usage` | Yes | Derived from guest memory stats (`available - usable`) so it reflects in-guest usage rather than the flavor allocation. |
| `instance` | `memory.maximum` | `memory` | Yes | `memory` is a compatibility alias over the allocated-memory series. |
| `instance` | `memory.available` | `memory.available` | Yes | Direct name match. |
| `instance` | `memory.usable` | none | Not applicable | Proxy-only metric from libvirt exporter data. |
| `instance` | `memory.rss` | `memory.resident` | Yes | `memory.resident` is a compatibility alias over the same RSS-backed series. |
| `instance` | `memory.used_percent` | none | Not applicable | Proxy-only metric. |
| `instance` | `disk.read.bytes` | `disk.device.read.bytes` | Yes | `disk.device.read.bytes` is a compatibility alias over the same instance-scoped aggregated read series. |
| `instance` | `disk.write.bytes` | `disk.device.write.bytes` | Yes | `disk.device.write.bytes` is a compatibility alias over the same instance-scoped aggregated write series. |
| `instance` | `disk.read.bytes.rate` | `disk.device.read.bytes.rate` | Yes | Read throughput in `B/s`, calculated from `libvirt_domain_block_stats_read_bytes_total`. |
| `instance` | `disk.write.bytes.rate` | `disk.device.write.bytes.rate` | Yes | Write throughput in `B/s`, calculated from `libvirt_domain_block_stats_write_bytes_total`. |
| `instance` | `disk.read.requests` | `disk.device.read.requests` | Yes | Cumulative read I/O requests from `libvirt_domain_block_stats_read_requests_total`. |
| `instance` | `disk.write.requests` | `disk.device.write.requests` | Yes | Cumulative write I/O requests from `libvirt_domain_block_stats_write_requests_total`. |
| `instance` | `disk.read.requests.rate` | `disk.device.read.requests.rate` | Yes | Read IOPS in `request/s`, calculated from the read-request counter. |
| `instance` | `disk.write.requests.rate` | `disk.device.write.requests.rate` | Yes | Write IOPS in `request/s`, calculated from the write-request counter. |
| `instance` | `disk.capacity` | `disk.device.capacity` | Yes | `disk.device.capacity` is a compatibility alias over the same instance-scoped aggregated capacity series. |
| `instance` | `network.incoming.bytes` | `network.incoming.bytes` | Yes | Direct name match. |
| `instance` | `network.outgoing.bytes` | `network.outgoing.bytes` | Yes | Direct name match. |
| `instance` | `status` | none | Not applicable | Proxy-only status metric from Nova state data. |
| `instance` | `local_gb` | none | Not applicable | Proxy-only capacity metric from Nova state data. |
| `volume` | `volume.size` | `volume.size` | Yes | Direct name match. |
| `volume` | `volume.status` | none | Not applicable | Proxy-only status metric. |
| `network` | `network.present` | none | Not applicable | Proxy-only presence metric. |
| `network` | `network.status` | none | Not applicable | Proxy-only status metric. |
| `port` | `port.present` | none | Not applicable | Proxy-only presence metric. |
| `port` | `port.status` | none | Not applicable | Proxy-only status metric. |

The closest-match names in this checklist are based on the OpenStack Ceilometer measurements reference: [Measurements](https://docs.openstack.org/ceilometer/latest/admin/telemetry-measurements.html).

Both the proxy-native names and the Gnocchi-compatible aliases appear in `GET /v1/metric`. The `disk.device.*` aliases keep the familiar Gnocchi names, but today they return one instance-scoped aggregate per VM rather than a true per-device breakdown. Thus, for a VM with only its root disk attached, the throughput and IOPS values represent the root disk; with multiple disks attached, they are the sum across all attached disks.

### Disk-throughput API URLs

`DISK_THROUGHPUT` is an application-level category, not a Gnocchi metric name. A Gnocchi client resolves the standard metric ID from the instance resource and requests its measures using either of these supported URLs:

```http
GET /v1/resource/instance/<instance-uuid>
X-Auth-Token: <token>
```

The resource response contains `metrics.disk.device.read.bytes.rate` and `metrics.disk.device.write.bytes.rate`. Use either returned metric UUID with:

```http
GET /v1/metric/<metric-uuid>/measures?start=<rfc3339>&stop=<rfc3339>&granularity=300&aggregation=mean
X-Auth-Token: <token>
```

or use the name-based equivalent:

```http
GET /v1/resource/instance/<instance-uuid>/metric/disk.device.read.bytes.rate/measures?start=<rfc3339>&stop=<rfc3339>&granularity=300&aggregation=mean
X-Auth-Token: <token>
```

The write-throughput URL substitutes `disk.device.write.bytes.rate`. For IOPS, use `disk.device.read.requests.rate` or `disk.device.write.requests.rate` in the same URL shape.

### Instance device resource searches

The proxy supports Gnocchi's read-only device-resource searches. It derives each resource from the libvirt `domain` and `target_device` labels and associates it with the Nova UUID supplied by `libvirt_domain_openstack_info`. The resource's `original_resource_id` is `<instance-id>-<device>`; its returned `id` is a stable UUID.

```http
POST /v1/search/resource/instance_network_interface
Content-Type: application/json

{"=": {"instance_id": "<nova-instance-uuid>"}}
```

```http
POST /v1/search/resource/instance_disk
Content-Type: application/json

{"=": {"instance_id": "<nova-instance-uuid>"}}
```

These endpoints only query the in-memory catalog. Resource creation, update, and deletion remain unsupported.

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
openstack user create --domain service --password '<PASSWORD>' gnocchi-proxy
openstack role add --project service --user gnocchi-proxy --user-domain service admin
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

For Atmosphere deployments, the OpenStack metadata series typically come from the `openstack-exporter` ServiceMonitor, so `config.prometheus.database_selector` is usually `job="openstack-exporter"`.

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
kubectl port-forward -n openstack svc/gnocchi-proxy-api 8080:8080
curl -s http://127.0.0.1:8080/healthz
```

## CI and Release

- `.github/workflows/ci.yml` runs `go test`, `go build`, `helm lint`, `helm template`, and `docker build`.
- `.github/workflows/release.yml` builds tagged binaries, publishes release archives, and pushes multi-arch images to GHCR.
