# Monitoring ArgoCD Agent with Grafana Dashboards

This directory contains pre-built Grafana dashboards for monitoring the argocd-agent components:

| Dashboard | File | Description |
|-----------|------|-------------|
| **Principal** | `argocd-agent-principal-dashboard.json` | Monitors the principal component on the control-plane cluster |
| **Agent** | `argocd-agent-agent-dashboard.json` | Monitors agent components on any cluster (control-plane or workload) |

- **Control-plane admins** see everything: principal metrics and all agent metrics (pulled from workload clusters via exposed Kubernetes Services).
- **Workload cluster users** see only their local agent metrics.

---

## 1. Setting Up Prometheus and Grafana

Before importing dashboards, you need Prometheus and Grafana running on the cluster. If you already have them, skip to section 2.

### 1.1 Deploy Prometheus

**Option A: Using Helm (recommended)**

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm install prometheus prometheus-community/prometheus \
  --namespace monitoring --create-namespace \
  --set server.persistentVolume.enabled=false \
  --set alertmanager.enabled=false
```

**Option B: Using a static configuration file**

Create a `prometheus.yml` file (examples in later sections) and run Prometheus with it (e.g., in Docker or as a standalone binary).

### 1.2 Deploy Grafana

```bash
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

helm install grafana grafana/grafana \
  --namespace monitoring --create-namespace \
  --set adminPassword='<your-password>'
```

The default username is `admin`.

### 1.3 Add Prometheus as a Grafana data source

1. Open Grafana UI
2. Go to **Connections** → **Data sources** → **Add data source**
3. Select **Prometheus**
4. Set the URL to your Prometheus server (e.g., `http://prometheus-server.monitoring.svc.cluster.local:80`)
5. Click **Save & Test**

### 1.4 How to add scrape targets to Prometheus

Prometheus discovers what to monitor via **scrape targets** in its configuration. How you add them depends on your deployment:

#### Helm chart deployment

If you installed Prometheus via the `prometheus-community/prometheus` Helm chart, scrape configuration lives in a ConfigMap (typically `prometheus-server`). You can either:

- Edit the ConfigMap directly:

```bash
kubectl edit configmap prometheus-server -n monitoring
```

- Or set scrape configs in your Helm `values.yaml` and upgrade:

```yaml
# values.yaml
serverFiles:
  prometheus.yml:
    scrape_configs:
      - job_name: 'example'
        static_configs:
          - targets: ['<service-address>:<port>']
```

```bash
helm upgrade prometheus prometheus-community/prometheus -n monitoring -f values.yaml
```

#### Static config file deployment

If you run Prometheus with a `prometheus.yml` file (e.g., in Docker), add scrape targets directly to that file and restart Prometheus or send a reload signal:

```bash
curl -X POST http://localhost:9090/-/reload
```

(Requires the `--web.enable-lifecycle` flag.)

#### Prometheus Operator (ServiceMonitor)

If you use the [Prometheus Operator](https://prometheus-operator.dev/) (common with `kube-prometheus-stack` Helm chart), you don't edit Prometheus config directly. Instead, create `ServiceMonitor` resources and the operator configures Prometheus automatically.

> **Tip**: To verify Prometheus is scraping correctly, open the Prometheus UI at `http://<prometheus-host>:9090/targets` and confirm targets show as `UP`.

### 1.5 How to import dashboards into Grafana

You can import dashboard JSON files via:

- **Grafana UI**: Go to **Dashboards** → **Import** → **Upload JSON file** → select the Prometheus data source → **Import**
- **Grafana API**:

```bash
curl -X POST http://<grafana-host>:3000/api/dashboards/db \
  -H "Content-Type: application/json" \
  -u admin:<password> \
  -d "{\"dashboard\": $(cat <dashboard-file>.json), \"overwrite\": true}"
```

---

## 2. Principal Dashboard on the Control-Plane

This sets up monitoring for the principal component. Only needed on the control-plane cluster.

### 2.1 Enable metrics on the principal

The principal exposes a Prometheus-compatible `/metrics` endpoint. Enable it by setting the metrics port.

**Using ConfigMap** (`argocd-agent-params`):

```yaml
data:
  principal.metrics.port: "8000"
```

**Using CLI flag:**

```bash
argocd-agent principal --metrics-port 8000
```

**Using environment variable:**

```bash
ARGOCD_PRINCIPAL_METRICS_PORT=8000
```

The default port is `8000`. A Kubernetes Service named `argocd-agent-principal-metrics` is included in the install manifests (`install/kubernetes/principal/principal-metrics-service.yaml`) and exposes port `8000`.

### 2.2 Configure Prometheus to scrape the principal

Add the principal as a scrape target in Prometheus (see section 1.4 for how to edit your Prometheus config):

```yaml
scrape_configs:
  - job_name: 'argocd-agent-principal'
    static_configs:
      - targets: ['argocd-agent-principal-metrics.argocd.svc.cluster.local:8000']
        labels:
          namespace: 'argocd'
          job: 'argocd-agent-principal'
```

> **Important**: For static targets, add `namespace` and `job` labels as shown above. The principal dashboard queries filter by these labels.

If using the Prometheus Operator, create a `ServiceMonitor`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: argocd-agent-principal
  namespace: argocd
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: argocd-agent-principal
  endpoints:
    - port: metrics
      interval: 15s
```

### 2.3 Import the principal dashboard

Import `argocd-agent-principal-dashboard.json` into Grafana (see section 1.5).

---

## 3. Agent Dashboard on a Workload Cluster

This sets up monitoring for an agent on its own workload cluster. Users on the workload cluster see only their local agent's data.

### 3.1 Enable metrics on the agent

**Using ConfigMap** (`argocd-agent-params`):

```yaml
data:
  agent.metrics.port: "8181"
```

**Using CLI flag:**

```bash
argocd-agent agent --metrics-port 8181
```

**Using environment variable:**

```bash
ARGOCD_AGENT_METRICS_PORT=8181
```

The default port is `8181`. A Kubernetes Service named `argocd-agent-agent-metrics` is included in the install manifests (`install/kubernetes/agent/agent-metrics-service.yaml`) and exposes port `8181`.

### 3.2 Configure Prometheus to scrape the local agent

Add the agent as a scrape target in the workload cluster's Prometheus (see section 1.4):

```yaml
scrape_configs:
  - job_name: 'argocd-agent-agent'
    static_configs:
      - targets: ['argocd-agent-agent-metrics.argocd.svc.cluster.local:8181']
        labels:
          namespace: 'argocd'
          agent_name: '<agent-name>'
          job: 'argocd-agent-agent'
```

Replace `<agent-name>` with a descriptive name for this agent (e.g., `production-us-east`, `staging-eu`).

> **Important**: For static targets, `agent_name`, `namespace`, and `job` labels are required. The agent dashboard uses these labels for filtering.

### 3.3 Import the agent dashboard

Import `argocd-agent-agent-dashboard.json` into the workload cluster's Grafana (see section 1.5). Since the local Prometheus only scrapes the local agent, the dashboard will show only that agent's data.

---

## 4. Agent Dashboard on the Control-Plane

This lets control-plane admins view agent metrics from all workload clusters in a single dashboard.

> **How it works**: The control-plane Prometheus scrapes each agent's `/metrics` endpoint directly and stores its own copy of the data. If you also have a Prometheus instance on the workload cluster (section 3), both instances independently collect and store the same agent metrics. The same `argocd-agent-agent-dashboard.json` file is used in both places; the difference is what each Prometheus scrapes.

### 4.1 Enable metrics on each agent

Ensure each agent on every workload cluster has metrics enabled (same as section 3.1).

### 4.2 Expose agent metrics to the control-plane

The agent metrics Service on each workload cluster must be reachable from the control-plane cluster's Prometheus. Common approaches:

- **LoadBalancer Service**: Change the agent metrics Service type to `LoadBalancer`
- **Ingress/Route**: Expose the metrics endpoint via an Ingress or OpenShift Route
- **Multi-cluster networking**: Use a service mesh or multi-cluster DNS

### 4.3 Configure control-plane Prometheus to scrape agents

Add each agent as a scrape target in the control-plane Prometheus (see section 1.4). Use the `agent_name` label to give each agent a human-readable name — this populates the dashboard's "Agent Name" dropdown:

```yaml
scrape_configs:
  # Principal (already configured in section 2.2)
  - job_name: 'argocd-agent-principal'
    static_configs:
      - targets: ['argocd-agent-principal-metrics.argocd.svc.cluster.local:8000']
        labels:
          namespace: 'argocd'
          job: 'argocd-agent-principal'

  # Agent on workload cluster A
  - job_name: 'argocd-agent-cluster-a'
    static_configs:
      - targets: ['<cluster-a-agent-external-address>:8181']
        labels:
          namespace: 'argocd'
          agent_name: 'cluster-a'
          job: 'argocd-agent-cluster-a'

  # Agent on workload cluster B
  - job_name: 'argocd-agent-cluster-b'
    static_configs:
      - targets: ['<cluster-b-agent-external-address>:8181']
        labels:
          namespace: 'argocd'
          agent_name: 'cluster-b'
          job: 'argocd-agent-cluster-b'
```

Replace the addresses with the actual externally reachable addresses from section 4.2. Choose `agent_name` values that clearly identify each workload cluster.

> **Important**: For static targets, `agent_name`, `namespace`, and `job` labels are required. The agent dashboard filters metrics using these labels.

If using the Prometheus Operator, create a `ServiceMonitor` per agent with `relabelings` to set the `agent_name` label.

### 4.4 Import the agent dashboard

Import `argocd-agent-agent-dashboard.json` into the control-plane Grafana (see section 1.5). The "Agent Name" dropdown will show all configured agents, letting you filter by individual agent or view all at once.

---

## Dashboard Variables

Both dashboards support the following template variables (configurable via dropdowns at the top of the dashboard):

| Variable | Dashboard | Description |
|----------|-----------|-------------|
| `datasource` | Both | Prometheus data source to query |
| `namespace` | Both | Filter by Kubernetes namespace |
| `agent_name` | Both | Filter by agent name (set via Prometheus `agent_name` label) |

---

## Metrics Reference

For a complete list of all metrics, labels, and descriptions, see [docs/operations/metrics.md](../../docs/operations/metrics.md).

### Principal Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `argocd_agent_build_info` | Gauge | Build metadata with `version` and `git_revision` labels |
| `agent_connected_with_principal` | Gauge | Number of connected agents |
| `principal_agent_avg_connection_time` | Gauge | Average agent connection duration (minutes) |
| `argocd_principal_agent_connections_total` | Counter | Total successful connections per agent (label: `agent_name`) |
| `principal_applications_created` | Counter | Total applications created |
| `principal_applications_updated` | Counter | Total applications updated |
| `principal_applications_deleted` | Counter | Total applications deleted |
| `principal_app_projects_created` | Counter | Total app projects created |
| `principal_app_projects_updated` | Counter | Total app projects updated |
| `principal_app_projects_deleted` | Counter | Total app projects deleted |
| `principal_repositories_created` | Counter | Total repositories created |
| `principal_repositories_updated` | Counter | Total repositories updated |
| `principal_repositories_deleted` | Counter | Total repositories deleted |
| `argocd_principal_appsets_created` | Counter | Total ApplicationSets created |
| `argocd_principal_appsets_updated` | Counter | Total ApplicationSets updated |
| `argocd_principal_appsets_deleted` | Counter | Total ApplicationSets deleted |
| `argocd_principal_gpg_keys_count` | Gauge | Current GPG key count |
| `principal_events_received` | Counter | Total events received from agents |
| `principal_events_sent` | Counter | Total events sent to agents |
| `principal_event_processing_time` | Histogram | Event processing latency (labels: `status`, `agent_name`, `resource_type`) |
| `principal_event_writer_send_errors_total` | Counter | Total EventWriter send errors (labels: `agent_name`, `reason`) |
| `argocd_principal_event_writer_events_discarded_total` | Counter | Total events discarded after exhausting retries (labels: `agent_name`, `event_type`, `resource_type`) |
| `principal_errors` | Counter | Total errors (label: `resource_type`) |
| `argocd_principal_resource_proxy_requests_total` | Counter | Total resource proxy requests (label: `agent_name`) |
| `argocd_principal_resource_proxy_errors_total` | Counter | Total resource proxy failures (labels: `agent_name`, `reason`) |
| `argocd_principal_redis_proxy_requests_total` | Counter | Total Redis proxy requests (labels: `agent_name`, `command`) |
| `argocd_principal_redis_proxy_errors_total` | Counter | Total Redis proxy failures (labels: `agent_name`, `command`) |

### Agent Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `argocd_agent_build_info` | Gauge | Build metadata with `version` and `git_revision` labels |
| `argocd_agent_connection_status` | Gauge | Connection status (1 = connected, 0 = disconnected) |
| `argocd_agent_connection_start_timestamp_seconds` | Gauge | Unix timestamp of current connection start |
| `argocd_agent_connections_total` | Counter | Total successful connections to the principal |
| `argocd_agent_auth_failures_total` | Counter | Total authentication failures |
| `agent_events_received` | Counter | Total events received from principal |
| `agent_events_sent` | Counter | Total events sent to principal |
| `agent_event_processing_time` | Histogram | Event processing latency (labels: `status`, `agent_mode`, `resource_type`) |
| `agent_event_propagation_latency_seconds` | Histogram | Time from principal send to agent processing (label: `resource_type`) |
| `argocd_agent_event_writer_events_discarded_total` | Counter | Total events discarded after exhausting retries (labels: `event_type`, `resource_type`) |
| `agent_errors` | Counter | Total errors (label: `resource_type`) |
| `argocd_agent_resource_proxy_requests_total` | Counter | Total resource proxy requests processed |
| `argocd_agent_resource_proxy_errors_total` | Counter | Total resource proxy failures |
| `argocd_agent_redis_proxy_requests_total` | Counter | Total Redis proxy requests (label: `command`) |
| `argocd_agent_redis_proxy_errors_total` | Counter | Total Redis proxy failures (label: `command`) |
