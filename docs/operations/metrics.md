# Metrics
The argocd-agent exposes different sets of Prometheus metrics for agent and principal components.

Metrics are by default enabled in both principal and agent but they will be disabled if metrics port is set to `0`.

Metrics for principal are exposed at `http://0.0.0.0:8000/metrics` endpoint. Users can overwrite metrics port by setting `--metrics-port` flag in CLI or `ARGOCD_PRINCIPAL_METRICS_PORT` environment variable.

Similarly for agent metrics are exposed at `http://0.0.0.0:8181/metrics` endpoint and port can be overwritten by the setting `--metrics-port` flag in CLI or `ARGOCD_AGENT_METRICS_PORT` environment variable.

Here is the list of available metrics:

### Principal Metrics

#### General

| Metric | Type | Labels | Description |
|--------|:----:|--------|-------------|
| `agent_connected_with_principal` | gauge | | Number of agents currently connected to the principal. |
| `principal_agent_avg_connection_time` | gauge | | Average duration all connected agents have been connected (minutes). |
| `principal_applications_created` | counter | | Total applications created by agents on the control plane. |
| `principal_applications_updated` | counter | | Total applications updated by agents on the control plane. |
| `principal_applications_deleted` | counter | | Total applications deleted by agents on the control plane. |
| `principal_app_projects_created` | counter | | Total AppProjects created by agents on the control plane. |
| `principal_app_projects_updated` | counter | | Total AppProjects updated by agents on the control plane. |
| `principal_app_projects_deleted` | counter | | Total AppProjects deleted by agents on the control plane. |
| `principal_events_received` | counter | | Total events received from agents. |
| `principal_events_sent` | counter | | Total events sent to agents. |
| `principal_event_processing_time` | histogramVec | `status`, `agent_name`, `resource_type` | Time taken to process inbound events (seconds). |
| `principal_errors` | counterVec | `resource_type` | Total errors occurred in principal. |

#### Hop-by-Hop Event Latency

These metrics instrument each stage of the principal → agent event delivery pipeline, making it possible to identify where latency accumulates when propagation is slow.

```
principal send queue  →  event writer  →  wire send  →  agent  →  ACK received
        ↑                     ↑                              ↑
  SendQueueDwell        EventWriterDwell               AckRoundtrip
```

| Metric | Type | Labels | Description |
|--------|:----:|--------|-------------|
| `principal_send_queue_dwell_seconds` | histogramVec | `resource_type` | Time an outbound event spends in the principal send queue from enqueue until the event writer picks it up. Elevated values indicate the send goroutine or event writer are a bottleneck. Buckets: 1ms–60s. |
| `principal_event_writer_dwell_seconds` | histogramVec | `resource_type` | Time an outbound event spends inside the event writer from when it is added until it is sent on the wire. The event writer polls every 100ms, so a baseline of 0–100ms is expected. Sustained elevation indicates gRPC stream backpressure or a large number of resources cycling through the writer. Buckets: 1ms–60s. |
| `principal_ack_roundtrip_seconds` | histogramVec | `resource_type` | Time from when an event is written to the wire until the corresponding ACK is received back from the agent. Measures network latency plus agent-side processing time. If no new observations appear, ACKs have stopped (agent disconnected or events are being dropped after max retries). Buckets: 1ms–60s. |

#### Kubernetes API Client Metrics

Standard `client-go` metrics exposed under the conventional `rest_client_*` names. Useful for detecting Kubernetes API server rate limiting, which can cause principal or agent to fall behind on reconciliation.

| Metric | Type | Labels | Description |
|--------|:----:|--------|-------------|
| `rest_client_request_duration_seconds` | histogramVec | `verb`, `host` | Latency of outbound Kubernetes API requests. |
| `rest_client_rate_limiter_duration_seconds` | histogramVec | `verb`, `host` | Time spent waiting in the client-side rate limiter before a request is dispatched. Sustained values here indicate the agent or principal is being throttled by the Kubernetes API server. |
| `rest_client_requests_total` | counterVec | `code`, `method`, `host` | Total Kubernetes API requests, partitioned by HTTP status code, method, and host. |
| `rest_client_request_retries_total` | counterVec | `code`, `method`, `host` | Total Kubernetes API request retries, partitioned by HTTP status code, method, and host. A rising `429` count is the clearest signal of active rate limiting. |

### Agent Metrics

| Metric | Type | Labels | Description |
|--------|:----:|--------|-------------|
| `agent_events_received` | counter | | Total events received from the principal. |
| `agent_events_sent` | counter | | Total events sent to the principal. |
| `agent_event_processing_time` | histogramVec | `status`, `agent_mode`, `resource_type` | Time taken to process inbound events (seconds). |
| `agent_event_propagation_latency_seconds` | histogramVec | `resource_type` | Time from when the principal sent an event on the wire to when the agent begins processing it. Complements `principal_ack_roundtrip_seconds` to break down the ACK roundtrip into network transit and agent processing. |
| `agent_errors` | counterVec | `resource_type` | Total errors occurred in the agent. |

### Labels

| Label | Example | Description |
|-------|---------|-------------|
| `status` | `success` | Outcome of event processing. Values: `success`, `failure`, `discarded`, `not-allowed`. |
| `agent_name` | `agent-managed` | Name of the agent. |
| `agent_mode` | `managed` | Agent mode. Values: `managed`, `autonomous`. |
| `resource_type` | `application` | CloudEvent data schema identifying the resource type. Values: `application`, `appproject`, `repository`, `gpgkey`, `resource`, `resourceResync`, `redis`, `clusterCacheInfoUpdate`, `containerlog`, `heartbeat`, `terminal`, `applicationset`. |
| `verb` | `GET` | HTTP verb used in a Kubernetes API request. |
| `host` | `kubernetes.default.svc` | Target host of a Kubernetes API request. |
| `code` | `429` | HTTP response code from a Kubernetes API request. |
| `method` | `GET` | HTTP method of a Kubernetes API request. |
