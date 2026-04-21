# Metrics
The argocd-agent exposes different sets of Prometheus metrics for agent and principal components.

Metrics are by default enabled in both principal and agent but they will be disabled if metrics port is set to `0`.

Metrics for principal are exposed at `http://0.0.0.0:8000/metrics` endpoint. Users can overwrite metrics port by setting `--metrics-port` flag in CLI or `ARGOCD_PRINCIPAL_METRICS_PORT` environment variable.

Similarly for agent metrics are exposed at `http://0.0.0.0:8181/metrics` endpoint and port can be overwritten by the setting `--metrics-port` flag in CLI or `ARGOCD_AGENT_METRICS_PORT` environment variable.

Here is the list of available metrics:

### Principal Metrics
|   Metric  |   Type    |   Description |
|---------------------------------------------------|:---------:|---------------------------------------------------------------------------------------------------------------------------------------------|
|   `agent_connected_with_principal`    |   gauge   |   The total number of agents connected with principal.    |
|   `principal_agent_avg_connection_time`   |   gauge   |   The average time all agents are connected for (in minutes). |
|   `principal_applications_created`    |   counter |   The total number of applications created by agents. |
|   `principal_applications_updated`    |   counter |   The total number of applications updated by agents. |
|   `principal_applications_deleted`    |   counter |   The total number of applications deleted by agents. |
|   `principal_app_projects_created`    |   counter |   The total number of app project created by agents.  |
|   `principal_app_projects_updated`    |   counter |   The total number of app project updated by agents.  |
|   `principal_app_projects_deleted`    |	counter |   The total number of app project deleted by agents.  |
|   `principal_events_received` |	counter |   The total number of events sent by principal.   |
|   `principal_events_sent` |   counter |   The total number of events sent by principal.   |
|   `principal_event_processing_time`   |   histogramVec    |   Histogram of time taken to process events (in seconds). |
|   `principal_errors`  |	counterVec  |   The total number of errors occurred in principal.   |
|   `principal_event_queue_depth` |   gaugeVec   |   Current number of CloudEvents in the principal send or receive queue for a queue pair (per connected agent / client).   |
|   `principal_event_writer_sent_pending` |   gaugeVec   |   Resources with a CloudEvent sent to the agent and awaiting ACK or resend (principal EventWriter).   |
|   `principal_event_writer_resend_due` |   gaugeVec   |   Subset of `sent_pending` where the retry timer has elapsed and a resend may run.   |
|   `principal_event_writer_resend_backoff_wait` |   gaugeVec   |   Subset of `sent_pending` waiting on backoff before the next resend attempt.   |
|   `principal_event_writer_retries_exhausted_drop_total` |   counterVec   |   CloudEvents dropped after exhausting send retries (principal to agent stream).   |

### Agent Metrics
|   Metric  |   Type    |   Description |
|---------------------------------------------------|:---------:|---------------------------------------------------------------------------------------------------------------------------------------------|
|   `agent_events_received` |   counter |   The total number of events received by agent.   |
|   `agent_events_sent` |   counter |   The total number of events sent by agent.   |
|   `agent_event_processing_time`   |	histogramVec    | Histogram of time taken to process events (in seconds).   |
|   `agent_errors`  |   counterVec	| The total number of errors occurred in agent. |
|   `agent_event_queue_depth` |   gaugeVec   |   Current number of CloudEvents in the agent send or receive queue for a queue pair.   |
|   `agent_event_writer_sent_pending` |   gaugeVec   |   Resources with a CloudEvent sent to the principal and awaiting ACK or resend (agent EventWriter).   |
|   `agent_event_writer_resend_due` |   gaugeVec   |   Subset of `sent_pending` where the retry timer has elapsed and a resend may run.   |
|   `agent_event_writer_resend_backoff_wait` |   gaugeVec   |   Subset of `sent_pending` waiting on backoff before the next resend attempt.   |
|   `agent_event_writer_retries_exhausted_drop_total` |   counterVec   |   CloudEvents dropped after exhausting send retries (agent to principal stream).   |

Here is the list of available labels:

### Labels
|   Label Name  |   Example Value   |   Description |
|--------------------|---------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|   `call_status` | success   |   Status of event processing. Possible values are: success, failure, discarded, not-allowed.  |
|   `agent_name`  |   agent-managed   |   Name of Agent. Possible values are: agent-managed, agent-autonomous.    |
|   `resource_type`   |   application |   Type of resource. Possible values are: application, app project, resource, resourceResync.   |
|   `queue`   |   default / agent name   |   Queue pair key: on the agent this is typically `default`; on the principal it identifies the agent or client queue pair.   |
|   `direction`   |   send / recv   |   Whether the depth is for the outbound (send) or inbound (receive) queue.   |
|   `agent`   |   client id / empty   |   Agent (client) id for principal EventWriter metrics; empty string for the single agent outbound writer counters/gauges.   |
