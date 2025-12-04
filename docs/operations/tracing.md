# OpenTelemetry Tracing

The argocd-agent supports distributed tracing using OpenTelemetry to help you understand the flow of resources from the principal to agents and identify performance bottlenecks. The tracing integration uses OpenTelemetry with the OTLP (OpenTelemetry Protocol) exporter, which can send traces to any OTLP-compatible backend such as Jaeger, Grafana Tempo, Datadog, New Relic, etc.

## Overview

Tracing provides visibility into:
- **gRPC Communication**: All gRPC calls between principal and agents are automatically traced
- **Resource Synchronization**: Track when resources are created on the principal and synced to agents
- **Event Processing**: Monitor the processing of events in both principal and agent
- **Kubernetes Operations**: Observe Kubernetes API calls made by the system

## Quick Start with Jaeger

The easiest way to get started is to use Jaeger for local development.

### 1. Start Jaeger

Using Docker:

```bash
docker run -d --name jaeger \
  -p 16686:16686 \
  -p 4317:4317 \
  -p 4318:4318 \
  jaegertracing/all-in-one:latest
```

### 2. Enable Tracing in Principal

Start the principal with tracing enabled:

```bash
argocd-agent principal \
  --otlp-address=localhost:4317 \
  --otlp-insecure=true \
  # ... other flags
```

Or using environment variables:

```bash
export ARGOCD_PRINCIPAL_OTLP_ADDRESS=localhost:4317
export ARGOCD_PRINCIPAL_OTLP_INSECURE=true

argocd-agent principal # ... other flags
```

### 3. Enable Tracing in Agent

Start the agent with tracing enabled:

```bash
argocd-agent agent \
  --otlp-address=localhost:4317 \
  --otlp-insecure=true \
  # ... other flags
```

Or using environment variables:

```bash
export ARGOCD_AGENT_OTLP_ADDRESS=localhost:4317
export ARGOCD_AGENT_OTLP_INSECURE=true

argocd-agent agent # ... other flags
```

### 4. View Traces

Open Jaeger UI at http://localhost:16686 and select:
- Service: `principal` or `agent`
- Click "Find Traces" to see the traces

## Configuration Options

| Flag | Environment Variable | Default | Description |
|------|---------------------|---------|-------------|
| `--otlp-address` | `ARGOCD_PRINCIPAL_OTLP_ADDRESS` | `localhost:4317` | OTLP collector endpoint address |
| `--otlp-insecure` | `ARGOCD_PRINCIPAL_OTLP_INSECURE` | `false` | Use insecure connection to OTLP endpoint |

## Trace Attributes

The following custom attributes are added to spans to provide context:

### Common Attributes

- `argocd.component.type`: Component from which the event originates
- `argocd.agent.name`: Name of the agent
- `argocd.agent.mode`: Mode of the agent (autonomous or managed)

### Resource Attributes

- `k8s.resource.kind`: Kubernetes resource kind (Application, AppProject, Repository)
- `k8s.resource.name`: Resource name
- `k8s.resource.namespace`: Resource namespace
- `k8s.resource.uid`: Resource UID

### Event Attributes

- `argocd.event.type`: Type of event (Create, Update, Delete, etc.)
- `argocd.event.id`: Unique event identifier
- `argocd.operation.type`: Operation type (create, update, delete, get, list, etc.)

### Debugging with Traces

**Example 1: Why is my resource not syncing?**

Look for traces related to your application name. Check:
- Is the principal creating the event? (Look for event spans from the principal)
- Is the agent receiving the event? (Look for event spans from the agent)
- Is the event processing succeeding? (Check for error status in spans)

**Example 2: What's causing slow syncs?**

Sort traces by duration to find slow operations:
- Slow gRPC calls might indicate network issues
- Slow Kubernetes operations might indicate API server load
- Large span duration in event processing might indicate resource processing issues

### Security Considerations

- Use `--otlp-insecure=false` in production and configure proper TLS certificates
- Ensure your OTLP endpoint is properly secured and not exposed publicly
- Review trace data retention policies to comply with your data governance requirements

## Troubleshooting

### Traces not appearing in backend

1. Verify tracing is enabled: `--otlp-address`
2. Check the OTLP endpoint is correct and reachable
3. Check application logs for tracing initialization messages:
   ```
   OpenTelemetry tracing initialized (address=localhost:4317)
   ```
4. Verify your tracing backend is running and accepting OTLP data on the correct port

## Further Reading

- [OpenTelemetry Documentation](https://opentelemetry.io/docs/)
- [OTLP Specification](https://opentelemetry.io/docs/specs/otlp/)
- [Jaeger Documentation](https://www.jaegertracing.io/docs/)
- [Grafana Tempo Documentation](https://grafana.com/docs/tempo/)
