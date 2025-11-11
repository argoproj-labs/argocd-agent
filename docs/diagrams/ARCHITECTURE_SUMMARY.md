# ArgoCD Agent - Architecture Summary

This document provides a high-level overview of the argocd-agent architecture, referencing the detailed D2 diagrams for deeper understanding.

## Executive Summary

ArgoCD Agent is a distributed agent-based architecture that extends Argo CD's GitOps capabilities to hundreds or thousands of Kubernetes clusters. It solves the scalability challenges of traditional multi-cluster Argo CD deployments by:

1. **Reversing the connection model**: Agents connect to the control plane (not vice versa)
2. **Distributing compute**: Reconciliation happens on workload clusters
3. **Event-driven sync**: Lightweight message-based protocol instead of resource watching
4. **Network resilience**: Designed for unreliable networks, high latency, and disconnections
5. **Zero ingress**: Workload clusters require no inbound network access

## System Components

### Control Plane Cluster
- **Argo CD UI/API**: Standard Argo CD interface for management and observability
- **Principal Component**: Core argocd-agent component that:
  - Accepts agent connections via gRPC
  - Distributes configuration to agents (managed mode) or receives it (autonomous mode)
  - Aggregates status from all agents
  - Provides resource/redis proxy services
- **Optional Shared Components**: Redis and repository server (Pattern 1 integration)

📊 **Diagram**: [01-high-level-architecture.svg](./01-high-level-architecture.svg)

### Workload Cluster
- **Agent Component**: Lightweight component that:
  - Maintains persistent mTLS connection to principal
  - Receives configuration and sends status (managed mode) or vice versa (autonomous mode)
  - Manages local Argo CD resources
  - Handles resource proxy requests
- **Argo CD Controllers**: Application controller (required), optionally repo server and Redis
- **Application Workloads**: Actual deployed applications

📊 **Diagram**: [01-high-level-architecture.svg](./01-high-level-architecture.svg)

## Operating Modes

### Managed Mode
**Control plane is the source of truth for configuration**

- Users create/modify Applications on control plane
- Principal distributes to agents via event streams
- Agents apply locally and report status back
- Best for centralized control and management

**Data Flow**:
1. User creates Application in control plane namespace
2. Principal watches, generates CREATE event
3. Event sent to agent via gRPC stream
4. Agent creates Application in local cluster
5. Argo CD controller reconciles
6. Agent sends status updates back to principal

📊 **Diagram**: [02-managed-mode-dataflow.svg](./02-managed-mode-dataflow.svg)

### Autonomous Mode
**Workload clusters are the source of truth**

- Applications created on workload clusters (via Git, local API)
- Agents send configuration to principal for observability
- Control plane provides read-only view with limited operations
- Best for GitOps purists and autonomous environments

**Data Flow**:
1. GitOps process creates Application on workload cluster
2. Agent watches, generates CREATE event
3. Event sent to principal via gRPC stream
4. Principal creates mirror for observability
5. Status updates flow from agent to principal
6. Control plane can trigger sync/refresh operations only

📊 **Diagram**: [03-autonomous-mode-dataflow.svg](./03-autonomous-mode-dataflow.svg)

## Communication Protocol

### Event-Driven Architecture
- **CloudEvents format**: Standard envelope for all messages
- **Bidirectional gRPC streams**: Both parties can send/receive
- **Queue-based processing**: Per-agent inbox/outbox queues
- **Reliable delivery**: At-least-once semantics with idempotency

### Event Types
- **Configuration**: CREATE, UPDATE, DELETE (spec changes)
- **Status**: STATUS-UPDATE (health, sync status)
- **Sync**: REQUEST-SYNCED-RESOURCE-LIST, RESPONSE-SYNCED-RESOURCE
- **Control**: PING/PONG, PROCESSED (acknowledgments)

📊 **Diagram**: [05-component-internals.svg](./05-component-internals.svg)

## Security Architecture

### Trust Zones
1. **Control Plane Zone**: Trusted environment, hosts principal and Argo CD
2. **Workload Cluster Zones**: Semi-trusted, each isolated from others
3. **Internet/Untrusted**: All external threats

### Security Layers
1. **Network**: Firewall, network policies, zero ingress on workload clusters
2. **Transport**: mTLS for all agent-principal communication
3. **Authentication**: JWT tokens + client certificates (dual auth)
4. **Authorization**: Namespace isolation, RBAC, per-agent permissions
5. **Data**: Encryption in transit (mandatory), encryption at rest (optional)

### Key Security Features
- ✅ No credential sprawl (principal doesn't store workload cluster credentials)
- ✅ No cluster-to-cluster communication
- ✅ Agent initiates all connections (firewall-friendly)
- ✅ mTLS prevents man-in-the-middle attacks
- ✅ Namespace isolation limits blast radius

📊 **Diagram**: [04-security-boundaries.svg](./04-security-boundaries.svg)

## Resilience and Recovery

### Failure Scenarios Handled
1. **Agent Restart**: Automatic reconnection with resync
2. **Principal Restart**: State rebuilt from Kubernetes API or agents
3. **Network Partition**: Autonomous operation, resync on reconnection
4. **Missed Events**: Checksum-based drift detection and correction

### Resync Protocol
- **Checksum Calculation**: SHA256 of all resource keys (Kind/Name/UID)
- **Drift Detection**: Compare checksums between principal and agent
- **Delta Sync**: Only send changed resources
- **Full Sync**: Send all resources if major drift detected

### Recovery Guarantees
- **Eventual Consistency**: System always converges to correct state
- **Autonomous Operation**: Workload clusters continue during disconnection
- **State Preservation**: In-flight events preserved across reconnections
- **Idempotent Handlers**: Safe to process events multiple times

📊 **Diagram**: [06-resync-and-recovery.svg](./06-resync-and-recovery.svg)

## Resource Proxy

### Purpose
Enable control plane to access workload cluster resources without direct network connectivity.

### How It Works
1. User requests live resource (e.g., pod manifest) from UI
2. Request goes to "cluster shim" instead of real Kubernetes API
3. Cluster shim forwards to resource proxy
4. Resource proxy sends request to agent via gRPC tunnel
5. Agent queries local Kubernetes API
6. Response flows back through same tunnel
7. Displayed in Argo CD UI as if direct access

### Use Cases
- View live resource manifests
- Stream pod logs
- Execute commands in pods
- Perform custom resource actions
- Real-time status monitoring

### Similar: Redis Proxy
Same pattern for accessing workload cluster Redis cache from control plane Argo CD components.

📊 **Diagram**: [07-resource-proxy.svg](./07-resource-proxy.svg)

## Integration Patterns

### Pattern 1: Centralized Resources (Low Footprint)
- **Shared**: Repository server and Redis on control plane
- **Per-cluster**: Agent + Application controller only
- **Pros**: Minimal workload cluster footprint, centralized state
- **Cons**: Single point of failure, network dependency

### Pattern 2: Autonomous Clusters (Recommended)
- **Per-cluster**: Full Argo CD stack (agent, controllers, repo, Redis)
- **Shared**: Only Argo CD UI/API on control plane
- **Pros**: True autonomy, better performance, fault isolation
- **Cons**: Higher resource usage per cluster

📊 **Diagram**: [01-high-level-architecture.svg](./01-high-level-architecture.svg)

## Component Internals

### Principal Components
- **gRPC Layer**: Listener, server, interceptors (auth, metrics, logging)
- **API Services**: EventStream, Auth, Version APIs
- **Connection Management**: Agent tracking, queue management, resync tracking
- **Event Processing**: Callbacks, routing, validation
- **Resource Management**: Application, AppProject, Repository managers
- **Proxy Services**: Resource proxy, Redis proxy

### Agent Components
- **Connection Layer**: gRPC client, stream handler, reconnect logic
- **Event Handling**: Inbound/outbound handlers, filters
- **Resource Sync**: Application, AppProject, Repository managers
- **Resource Operations**: Resource handler, Redis handler

### Shared Packages
- **Backend Layer**: Pluggable storage (currently Kubernetes CRDs)
- **Infrastructure**: Auth, events, queues, cache, informers, metrics, logging
- **Utilities**: TLS, gRPC, Kubernetes, certificate issuer

📊 **Diagram**: [05-component-internals.svg](./05-component-internals.svg)

## Scalability Characteristics

### Control Plane Scaling
- **Concurrent Agents**: Hundreds to thousands
- **Event Throughput**: Thousands per second per agent
- **Memory**: O(agents × resources) for in-memory state
- **Horizontal Scaling**: Future work (requires shared state backend)

### Workload Cluster Scaling
- **Resources**: ~1-2 CPU cores, 2-4GB RAM for full stack
- **Network**: Minimal bandwidth (events only, not resource watching)
- **Latency**: Tolerates high latency and intermittent connections
- **Independence**: Each cluster scales independently

### Performance Tuning
- Queue sizes: Control memory vs buffering
- Processing concurrency: Balance throughput vs CPU
- Connection timeouts: Adjust for network conditions
- Resync intervals: Tune drift detection frequency

## Design Principles

1. **Agent Initiates**: All connections from agent to principal, never reverse
2. **Autonomous Operation**: Workload clusters function during disconnection
3. **Lightweight by Default**: Minimal dependencies, no mandatory databases
4. **Pluggable Backends**: Storage abstraction for future scalability
5. **Network Resilient**: No assumption of permanent, low-latency connectivity
6. **Zero Trust**: No cluster-to-cluster communication except via principal
7. **Argo CD Compatible**: Works with unmodified Argo CD installations

## Future Enhancements

- **Horizontal Principal Scaling**: Shared backend for multi-principal deployment
- **Alternative Transports**: Message bus (Kafka, NATS) instead of gRPC
- **Advanced RBAC**: Multi-tenancy, fine-grained permissions
- **Enhanced Observability**: Distributed tracing, advanced metrics
- **Pod Logs Streaming**: Real-time log aggregation
- **Additional Proxies**: Support for more Argo CD services

## Quick Reference

| Aspect | Managed Mode | Autonomous Mode |
|--------|--------------|-----------------|
| Configuration Source | Control Plane | Workload Cluster |
| Status Source | Workload Cluster | Workload Cluster |
| UI Operations | Full CRUD | Read + Limited Ops |
| GitOps Pattern | Centralized | Distributed (App-of-Apps) |
| Footprint | Minimal | Full Argo CD Stack |
| Autonomy | Depends on Control Plane | Fully Autonomous |
| Best For | Centralized Control | GitOps Purists, Edge |

## References

For detailed visual representation of each aspect:

1. **Overall Architecture**: [01-high-level-architecture.svg](./01-high-level-architecture.svg)
2. **Managed Mode Flow**: [02-managed-mode-dataflow.svg](./02-managed-mode-dataflow.svg)
3. **Autonomous Mode Flow**: [03-autonomous-mode-dataflow.svg](./03-autonomous-mode-dataflow.svg)
4. **Security Model**: [04-security-boundaries.svg](./04-security-boundaries.svg)
5. **Internal Components**: [05-component-internals.svg](./05-component-internals.svg)
6. **Failure Recovery**: [06-resync-and-recovery.svg](./06-resync-and-recovery.svg)
7. **Resource Access**: [07-resource-proxy.svg](./07-resource-proxy.svg)

See [README.md](./README.md) for instructions on viewing and generating these diagrams.
