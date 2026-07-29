# Metrics
The argocd-agent exposes different sets of Prometheus metrics for agent and principal components.

Metrics are by default enabled in both principal and agent but they will be disabled if metrics port is set to `0`.

Metrics for principal are exposed at `http://0.0.0.0:8000/metrics` endpoint. Users can overwrite metrics port by setting `--metrics-port` flag in CLI or `ARGOCD_PRINCIPAL_METRICS_PORT` environment variable.

Similarly for agent metrics are exposed at `http://0.0.0.0:8181/metrics` endpoint and port can be overwritten by the setting `--metrics-port` flag in CLI or `ARGOCD_AGENT_METRICS_PORT` environment variable.

Here is the list of available metrics:

## Build Metrics

| Metric | Type | Description |
|---|:-:|---|
| `argocd_agent_build_info` | gauge | Build metadata for the running argocd-agent binary. Labels: `version`, `git_revision`. |

## Principal Metrics

| Metric | Type | Description |
|---|:-:|---|
| `argocd_principal_connected_agents` | gauge | The total number of agents connected with principal. |
| `principal_agent_avg_connection_time` | gauge | The average time all agents are connected for (in minutes). |
| `argocd_principal_agent_connections_total` | counterVec | The total number of successful connections from each agent to the principal. |
| `argocd_principal_resync_requests_total` | counterVec | The total number of resync rounds requested by agents, by result (accepted or refused). |
| `principal_applications_created` | counter | The total number of applications created on the control plane. |
| `principal_applications_updated` | counter | The total number of applications updated on the control plane. |
| `principal_applications_deleted` | counter | The total number of applications deleted on the control plane. |
| `principal_app_projects_created` | counter | The total number of app projects created on the control plane. |
| `principal_app_projects_updated` | counter | The total number of app projects updated on the control plane. |
| `principal_app_projects_deleted` | counter | The total number of app projects deleted on the control plane. |
| `principal_repositories_created` | counter | The total number of repositories created on the control plane. |
| `principal_repositories_updated` | counter | The total number of repositories updated on the control plane. |
| `principal_repositories_deleted` | counter | The total number of repositories deleted on the control plane. |
| `argocd_principal_appsets_created` | counter | The total number of ApplicationSets created on the control plane. |
| `argocd_principal_appsets_updated` | counter | The total number of ApplicationSets updated on the control plane. |
| `argocd_principal_appsets_deleted` | counter | The total number of ApplicationSets deleted on the control plane. |
| `argocd_principal_gpg_keys_count` | gauge | The current number of GPG keys on the control plane. |
| `principal_events_received` | counter | The total number of events received by principal. |
| `principal_events_sent` | counter | The total number of events sent by principal. |
| `principal_event_processing_time` | histogramVec | Histogram of time taken to process events (in seconds). |
| `principal_event_writer_send_errors_total` | counterVec | The total number of EventWriter send errors observed by principal. |
| `argocd_principal_event_writer_events_discarded_total` | counterVec | The total number of events discarded by the EventWriter after exhausting retries. |
| `principal_errors` | counterVec | The total number of errors occurred in principal. |
| `argocd_principal_resource_proxy_requests_total` | counterVec | The total number of resource proxy requests received by principal. |
| `argocd_principal_resource_proxy_errors_total` | counterVec | The total number of resource proxy request failures on principal. |
| `argocd_principal_redis_proxy_requests_total` | counterVec | The total number of Redis proxy requests forwarded to agents. |
| `argocd_principal_redis_proxy_errors_total` | counterVec | The total number of Redis proxy request failures on principal. |

## Agent Metrics

| Metric | Type | Description |
|---|:-:|---|
| `argocd_agent_connection_status` | gauge | Whether the agent is currently connected to the principal (1 = connected, 0 = disconnected). |
| `argocd_agent_connection_start_timestamp_seconds` | gauge | Unix timestamp of when the current connection to the principal was established. |
| `argocd_agent_connections_total` | counter | The total number of successful connections from the agent to the principal. |
| `argocd_agent_auth_failures_total` | counter | The total number of authentication failures when connecting to the principal. |
| `agent_events_received` | counter | The total number of events received by agent. |
| `agent_events_sent` | counter | The total number of events sent by agent. |
| `agent_event_processing_time` | histogramVec | Histogram of time taken to process events (in seconds). |
| `agent_event_propagation_latency_seconds` | histogramVec | Histogram of time from principal send to agent processing (in seconds). |
| `argocd_agent_event_writer_events_discarded_total` | counterVec | The total number of events discarded by the EventWriter after exhausting retries. |
| `agent_errors` | counterVec | The total number of errors occurred in agent. |
| `argocd_agent_resource_proxy_requests_total` | counter | The total number of resource proxy requests processed by the agent. |
| `argocd_agent_resource_proxy_errors_total` | counter | The total number of resource proxy request failures on the agent. |
| `argocd_agent_redis_proxy_requests_total` | counterVec | The total number of Redis proxy requests processed by the agent. |
| `argocd_agent_redis_proxy_errors_total` | counterVec | The total number of Redis proxy request failures on the agent. |

### Labels

| Label Name | Example Value | Description |
|---|---|---|
| `status` | success | Status of event processing. Possible values: success, failure, discarded, not-allowed. |
| `agent_name` | agent-managed | Name of the agent. |
| `agent_mode` | managed | Mode of the agent. Possible values: managed, autonomous. |
| `resource_type` | application | Type of resource. Possible values: application, app project, resource, resourceResync. |
| `event_type` | create | Type of event. Possible values: create, delete, spec-update, status-update, etc. |
| `reason` | agent_disconnected | Reason for an error. Used in resource proxy and send error metrics. |
| `command` | get | Redis command type. Possible values: get, subscribe. |
| `version` | 0.1.0 | Application version. Used in `argocd_agent_build_info`. |
| `git_revision` | abc1234 | Git commit SHA. Used in `argocd_agent_build_info`. |
