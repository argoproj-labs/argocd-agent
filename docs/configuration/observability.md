# Observability

This document covers logging, metrics, profiling, and health check configuration for argocd-agent components.

## Logging

Both the principal and agent components support configurable logging levels and formats.

### Log Levels

| Level | Description | Use Case |
|-------|-------------|----------|
| `trace` | Most verbose, includes all details | Deep debugging |
| `debug` | Detailed information for debugging | Development, troubleshooting |
| `info` | Normal operational messages | Production (default) |
| `warn` | Warning conditions | Production |
| `error` | Error conditions only | Minimal logging |

### Principal Logging Configuration

```yaml
# ConfigMap (argocd-agent-params)
principal.log.level: "info"
```

Via command line:

```bash
argocd-agent principal --log-level=info --log-format=text
```

Via environment variable:

```bash
export ARGOCD_PRINCIPAL_LOG_LEVEL=info
```

### Agent Logging Configuration

```yaml
# ConfigMap (argocd-agent-params)
agent.log.level: "info"
```

Via command line:

```bash
argocd-agent agent --log-level=info --log-format=text
```

Via environment variable:

```bash
export ARGOCD_AGENT_LOG_LEVEL=info
```

### Log Formats

| Format | Description | Use Case |
|--------|-------------|----------|
| `text` | Human-readable text format | Development, manual inspection |
| `json` | Structured JSON format | Production, log aggregation |

**JSON Log Example:**

```json
{"level":"info","ts":"2024-01-15T10:30:45.123Z","msg":"Agent connected","agent":"production-cluster"}
```

**Text Log Example:**

```
INFO[0001] Agent connected                               agent=production-cluster
```

### Full-Detail Logging

Full-detail logging adds verbose resource payloads, diffs, and event data to structured log fields. It is independent of the log level and can be enabled per category.

**Categories:**

| Category | What it adds |
|----------|-------------|
| `actions` | Full JSON of created resources, diffs for updates (secrets are redacted) |
| `events` | Full CloudEvent payloads for messages exchanged between agent and principal |
| `informers` | Full JSON on add, diffs on update for K8s informer events (secrets are redacted) |

**Enable on the principal:**

```bash
# Single category
argocd-agent principal --full-detail=actions

# Multiple categories (comma-separated)
argocd-agent principal --full-detail=actions,events

# All categories
argocd-agent principal --full-detail=all
```

Via environment variable:

```bash
# Comma-separated list
export ARGOCD_PRINCIPAL_FULL_DETAIL="actions,events,informers"

# Or enable everything
export ARGOCD_PRINCIPAL_FULL_DETAIL="all"
```

**Enable on the agent:**

```bash
argocd-agent agent --full-detail=all
```

Via environment variable:

```bash
export ARGOCD_AGENT_FULL_DETAIL="actions,events"
```

**Example output with `--full-detail=actions`:**

```
level=info msg="Created application default/my-app" action=create detail="{\"metadata\":{\"name\":\"my-app\",...}}" log_category=actions name=my-app namespace=default resource_type=application
```

Without full detail, the same log omits the `detail` field:

```
level=info msg="Created application default/my-app" action=create log_category=actions name=my-app namespace=default resource_type=application
```

!!! note "Secret Redaction"
    Kubernetes Secrets are never included in the `detail` field. Create/add logs show `<redacted: Secret>` and update diffs are skipped entirely.

### Structured Log Fields

All action, event, and informer logs include structured fields for filtering and dashboard queries.

**Common fields across all categories:**

| Field | Description | Example values |
|-------|-------------|----------------|
| `log_category` | Log category | `actions`, `events`, `informers` |
| `action` | The operation performed | `create`, `update`, `delete` |
| `name` | Resource name | `my-app`, `default` |
| `namespace` | Resource namespace | `argocd`, `default` |

**Action-specific fields:**

| Field | Description | Example values |
|-------|-------------|----------------|
| `resource_type` | Kubernetes resource type | `application`, `appproject`, `secret`, `configmap` |
| `detail` | Full JSON or diff (when full detail enabled) | `{"metadata":...}` |

**Event-specific fields:**

| Field | Description | Example values |
|-------|-------------|----------------|
| `direction` | Event direction | `send`, `recv` |
| `event_target` | Target resource type | `application`, `appproject`, `repository` |
| `event_type` | CloudEvent type | `io.argoproj.argocd-agent.event.create` |
| `detail` | Event payload (when full detail enabled) | `{"metadata":...}` |

**Informer-specific fields:**

| Field | Description | Example values |
|-------|-------------|----------------|
| `resource_type` | Kubernetes resource type (lowercase) | `application`, `configmap`, `secret` |
| `detail` | Full JSON or diff (when full detail enabled) | `{"metadata":...}` |

### Debugging Tips

1. **Enable debug logging temporarily** to troubleshoot issues:
   ```bash
   kubectl set env deployment/argocd-agent-principal -n argocd ARGOCD_PRINCIPAL_LOG_LEVEL=debug
   ```

2. **View logs in real-time**:
   ```bash
   kubectl logs -f -n argocd deployment/argocd-agent-principal
   kubectl logs -f -n argocd deployment/argocd-agent-agent
   ```

3. **Filter logs by agent**:
   ```bash
   kubectl logs -n argocd deployment/argocd-agent-principal | grep "agent=my-cluster"
   ```

5. **Trace a resource through all categories**:
   ```bash
   kubectl logs -n argocd deployment/argocd-agent-agent | grep 'name=my-app' | grep -E 'log_category=(actions|events|informers)'
   ```

## Metrics

Both components expose Prometheus-compatible metrics for monitoring.

### Principal Metrics

**Configuration:**

```yaml
# ConfigMap (argocd-agent-params)
principal.metrics.port: "8000"
```

Via command line:

```bash
argocd-agent principal --metrics-port=8000
```

**Default Port:** 8000

**Endpoint:** `http://<principal-pod>:8000/metrics`

### Agent Metrics

**Configuration:**

```yaml
# ConfigMap (argocd-agent-params)
agent.metrics.port: "8181"
```

Via command line:

```bash
argocd-agent agent --metrics-port=8181
```

**Default Port:** 8181

**Endpoint:** `http://<agent-pod>:8181/metrics`

### Prometheus ServiceMonitor

Create a ServiceMonitor to scrape metrics:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: argocd-agent-principal
  namespace: argocd
spec:
  selector:
    matchLabels:
      app: argocd-agent-principal
  endpoints:
  - port: metrics
    interval: 30s
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: argocd-agent-agent
  namespace: argocd
spec:
  selector:
    matchLabels:
      app: argocd-agent-agent
  endpoints:
  - port: metrics
    interval: 30s
```

### Key Metrics

**Principal Metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `argocd_agent_connected_agents` | Gauge | Number of currently connected agents |
| `argocd_agent_grpc_requests_total` | Counter | Total gRPC requests by method |
| `argocd_agent_grpc_request_duration_seconds` | Histogram | gRPC request duration |
| `argocd_agent_sync_operations_total` | Counter | Total sync operations |

**Agent Metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `argocd_agent_connection_status` | Gauge | Connection status (1=connected, 0=disconnected) |
| `argocd_agent_reconnections_total` | Counter | Total reconnection attempts |
| `argocd_agent_events_sent_total` | Counter | Total events sent to principal |
| `argocd_agent_events_received_total` | Counter | Total events received from principal |

### Grafana Dashboard

For detailed metrics visualization, see the [Operations: Metrics](../operations/metrics.md) documentation.

## Health Checks

Both components provide health check endpoints for Kubernetes probes.

### Principal Health Checks

**Configuration:**

```yaml
# ConfigMap (argocd-agent-params)
principal.healthz.port: "8003"
```

Via command line:

```bash
argocd-agent principal --healthz-port=8003
```

**Default Port:** 8003

**Endpoints:**

| Endpoint | Purpose |
|----------|---------|
| `/healthz` | Liveness probe - is the process running? |
| `/readyz` | Readiness probe - is the component ready to serve traffic? |

### Agent Health Checks

**Configuration:**

```yaml
# ConfigMap (argocd-agent-params)
agent.healthz.port: "8001"
```

Via command line:

```bash
argocd-agent agent --healthz-port=8001
```

**Default Port:** 8001

**Endpoints:**

| Endpoint | Purpose |
|----------|---------|
| `/healthz` | Liveness probe |
| `/readyz` | Readiness probe |

### Kubernetes Probe Configuration

**Principal Deployment:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: argocd-agent-principal
spec:
  template:
    spec:
      containers:
      - name: principal
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8003
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8003
          initialDelaySeconds: 5
          periodSeconds: 5
```

**Agent Deployment:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: argocd-agent-agent
spec:
  template:
    spec:
      containers:
      - name: agent
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8001
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8001
          initialDelaySeconds: 5
          periodSeconds: 5
```

## Profiling

Both components support Go pprof profiling for performance analysis.

### Enabling Profiling

**Principal:**

```bash
argocd-agent principal --pprof-port=6060
```

Via environment variable:

```bash
export ARGOCD_PRINCIPAL_PPROF_PORT=6060
```

**Agent:**

```bash
argocd-agent agent --pprof-port=6060
```

Via environment variable:

```bash
export ARGOCD_AGENT_PPROF_PORT=6060
```

**Default:** Disabled (port 0)

!!! warning "Security Warning"
    Only enable profiling in development or when actively debugging. The pprof endpoint exposes sensitive runtime information.

### Using pprof

**Port-forward to the pprof endpoint:**

```bash
kubectl port-forward -n argocd deployment/argocd-agent-principal 6060:6060
```

**Collect a CPU profile:**

```bash
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
```

**Collect a heap profile:**

```bash
go tool pprof http://localhost:6060/debug/pprof/heap
```

**View goroutines:**

```bash
curl http://localhost:6060/debug/pprof/goroutine?debug=2
```

### Available pprof Endpoints

| Endpoint | Description |
|----------|-------------|
| `/debug/pprof/` | Index page |
| `/debug/pprof/heap` | Heap memory profile |
| `/debug/pprof/goroutine` | Stack traces of all goroutines |
| `/debug/pprof/profile` | CPU profile |
| `/debug/pprof/block` | Block profile |
| `/debug/pprof/mutex` | Mutex contention profile |
| `/debug/pprof/trace` | Execution trace |

For detailed profiling guidance, see the [Operations: Profiling](../operations/profiling.md) documentation.

## Configuration Summary

### Principal Observability Settings

| Parameter | CLI Flag | Env Variable | ConfigMap | Default |
|-----------|----------|--------------|-----------|---------|
| Log Level | `--log-level` | `ARGOCD_PRINCIPAL_LOG_LEVEL` | `principal.log.level` | `info` |
| Log Format | `--log-format` | `ARGOCD_PRINCIPAL_LOG_FORMAT` | N/A | `text` |
| Full Detail | `--full-detail` | `ARGOCD_PRINCIPAL_FULL_DETAIL` | N/A | disabled |
| Metrics Port | `--metrics-port` | `ARGOCD_PRINCIPAL_METRICS_PORT` | `principal.metrics.port` | `8000` |
| Health Port | `--healthz-port` | `ARGOCD_PRINCIPAL_HEALTH_CHECK_PORT` | `principal.healthz.port` | `8003` |
| Profiling Port | `--pprof-port` | `ARGOCD_PRINCIPAL_PPROF_PORT` | N/A | `0` (disabled) |

### Agent Observability Settings

| Parameter | CLI Flag | Env Variable | ConfigMap | Default |
|-----------|----------|--------------|-----------|---------|
| Log Level | `--log-level` | `ARGOCD_AGENT_LOG_LEVEL` | `agent.log.level` | `info` |
| Log Format | `--log-format` | `ARGOCD_PRINCIPAL_LOG_FORMAT` | N/A | `text` |
| Full Detail | `--full-detail` | `ARGOCD_AGENT_FULL_DETAIL` | N/A | disabled |
| Metrics Port | `--metrics-port` | `ARGOCD_AGENT_METRICS_PORT` | `agent.metrics.port` | `8181` |
| Health Port | `--healthz-port` | `ARGOCD_AGENT_HEALTH_CHECK_PORT` | `agent.healthz.port` | `8001` |
| Profiling Port | `--pprof-port` | `ARGOCD_AGENT_PPROF_PORT` | N/A | `0` (disabled) |

## Related Documentation

- [Networking](networking.md) - Transport protocols, compression, connection management
- [Operations: Metrics](../operations/metrics.md) - Detailed metrics documentation
- [Operations: Profiling](../operations/profiling.md) - Profiling guide
- [Reference: Principal](reference/principal.md) - Complete principal parameter reference
- [Reference: Agent](reference/agent.md) - Complete agent parameter reference
