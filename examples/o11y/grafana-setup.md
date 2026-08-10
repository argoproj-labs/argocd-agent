# Monitoring ArgoCD Agent with Grafana Dashboards

This directory contains example Grafana dashboards and ServiceMonitor resources for monitoring the argocd-agent components. These are starting points — adapt them to your own requirements.

| File | Description |
|------|-------------|
| `argocd-agent-principal-dashboard.json` | Grafana dashboard for the principal component |
| `argocd-agent-agent-dashboard.json` | Grafana dashboard for agent components |
| `principal-servicemonitor.yaml` | Example ServiceMonitor for Prometheus Operator (principal) |
| `agent-servicemonitor.yaml` | Example ServiceMonitor for Prometheus Operator (agent) |

- **Control-plane admins** see everything: principal metrics and all agent metrics (pulled from workload clusters via exposed Kubernetes Services).
- **Workload cluster users** see only their local agent metrics.

## Prerequisites

- A running Prometheus instance on each cluster where you want monitoring
- A running Grafana instance with Prometheus configured as a data source

---

## Principal Dashboard Setup

### Enable metrics on the principal

The principal exposes a Prometheus-compatible `/metrics` endpoint. The default port is `8000`.

**Using ConfigMap** (`argocd-agent-params`):

```yaml
data:
  principal.metrics.port: "8000"
```

**Using CLI flag:** `argocd-agent principal --metrics-port 8000`

**Using environment variable:** `ARGOCD_PRINCIPAL_METRICS_PORT=8000`

A Kubernetes Service named `argocd-agent-principal-metrics` is included in the install manifests and exposes port `8000`.

### Prometheus scrape configuration

Add the principal as a scrape target in your Prometheus configuration:

```yaml
scrape_configs:
  - job_name: 'argocd-agent-principal'
    static_configs:
      - targets: ['argocd-agent-principal-metrics.argocd.svc.cluster.local:8000']
        labels:
          namespace: 'argocd'
          job: 'argocd-agent-principal'
```

> **Note**: For static targets, add `namespace` and `job` labels as shown above. The dashboard queries filter by these labels.

If using the Prometheus Operator, apply the example ServiceMonitor provided in this directory:

```bash
kubectl apply -f principal-servicemonitor.yaml
```

### Import the dashboard

Import `argocd-agent-principal-dashboard.json` into Grafana.

---

## Agent Dashboard on a Workload Cluster

This sets up monitoring for an agent on its own workload cluster. Users see only their local agent's data.

### Enable metrics on the agent

The default port is `8181`.

**Using ConfigMap** (`argocd-agent-params`):

```yaml
data:
  agent.metrics.port: "8181"
```

**Using CLI flag:** `argocd-agent agent --metrics-port 8181`

**Using environment variable:** `ARGOCD_AGENT_METRICS_PORT=8181`

A Kubernetes Service named `argocd-agent-agent-metrics` is included in the install manifests and exposes port `8181`.

### Prometheus scrape configuration

Add the agent as a scrape target in the workload cluster's Prometheus:

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

> **Note**: For static targets, `agent_name`, `namespace`, and `job` labels are required. The agent dashboard uses these labels for filtering.

If using the Prometheus Operator, apply the example ServiceMonitor provided in this directory:

```bash
kubectl apply -f agent-servicemonitor.yaml
```

### Import the dashboard

Import `argocd-agent-agent-dashboard.json` into the workload cluster's Grafana.

---

## Agent Dashboard on the Control-Plane

This lets control-plane admins view agent metrics from all workload clusters in a single dashboard.

> **How it works**: The control-plane Prometheus scrapes each agent's `/metrics` endpoint directly and stores its own copy of the data. If you also have a Prometheus instance on the workload cluster, both instances independently collect and store the same agent metrics. The same `argocd-agent-agent-dashboard.json` file is used in both places; the difference is what each Prometheus instance scrapes.

### Expose agent metrics to the control-plane

The agent metrics Service on each workload cluster must be reachable from the control-plane Prometheus. Common approaches include LoadBalancer Services, Ingress, or multi-cluster networking.

### Prometheus scrape configuration

Add each agent as a scrape target in the control-plane Prometheus. Use the `agent_name` label to give each agent a human-readable name — this populates the dashboard's "Agent Name" dropdown:

```yaml
scrape_configs:
  # Principal (from previous section)
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

> **Note**: For static targets, `agent_name`, `namespace`, and `job` labels are required. The agent dashboard filters metrics using these labels.

### Import the dashboard

Import `argocd-agent-agent-dashboard.json` into the control-plane Grafana. The "Agent Name" dropdown will show all configured agents, letting you filter by individual agent or view all at once.

---

## Dashboard Variables

Both dashboards support the following template variables (configurable via dropdowns at the top):

| Variable | Dashboard | Description |
|----------|-----------|-------------|
| `datasource` | Both | Prometheus data source to query |
| `namespace` | Both | Filter by Kubernetes namespace |
| `agent_name` | Both | Filter by agent name (set via Prometheus `agent_name` label) |

---

## Metrics Reference

For a complete list of all metrics, labels, and descriptions, see `docs/operations/metrics.md`.
