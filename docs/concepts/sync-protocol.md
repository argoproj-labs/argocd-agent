# Synchronization Protocol

This document provides a detailed technical explanation of the synchronization protocol used by argocd-agent to maintain consistency between the principal (control plane) and agents (workload clusters). The target audience is engineers and architects who need to understand the underlying mechanisms.

## Overview

The argocd-agent synchronization protocol is built on a bidirectional streaming communication model that enables reliable data synchronization between a central principal and distributed agents. The protocol handles:

- **Configuration distribution** (managed mode) or **status aggregation** (autonomous mode)
- **Failure recovery** through resync mechanisms
- **Conflict detection** using checksums
- **Event-driven updates** with guaranteed ordering

## Architecture

### Communication Model

The protocol uses a **hub-and-spoke** architecture where:

- **Principal**: Runs on the control plane, acts as the central coordination point
- **Agents**: Run on workload clusters, initiate all connections to the principal
- **Connections**: Always initiated by agents (never principal-to-agent)
- **Streams**: Bidirectional gRPC streams over HTTP/2 (with HTTP/1.1 websocket fallback)

```
┌─────────────────┐    gRPC/HTTP2     ┌─────────────────┐
│   Agent 1       │──────────────────►│   Principal     │
│  (Workload)     │                   │ (Control Plane) │
└─────────────────┘                   └─────────────────┘
                                              ▲
┌─────────────────┐                          │
│   Agent 2       │──────────────────────────┘
│  (Workload)     │
└─────────────────┘
```

### Protocol Stack

```
┌─────────────────────────────────────┐
│         Application Events          │  ← Create, Update, Delete, Status
├─────────────────────────────────────┤
│        CloudEvents Format           │  ← Event envelope with metadata
├─────────────────────────────────────┤
│         gRPC Streaming              │  ← Bidirectional streams
├─────────────────────────────────────┤
│        HTTP/2 (or HTTP/1+WS)        │  ← Transport layer
├─────────────────────────────────────┤
│           TLS + mTLS                │  ← Security layer
└─────────────────────────────────────┘
```

## Communication Protocol

### gRPC Service Definition

The core communication is defined by the `EventStream` service:

```protobuf
service EventStream {
    rpc Subscribe(stream Event) returns (stream Event);
    rpc Push(stream Event) returns (PushSummary);
    rpc Ping(PingRequest) returns (PongReply);
}
```

### Connection Lifecycle

1. **Authentication**: Agent presents JWT token with client certificate (optional)
2. **Authorization**: Principal validates agent identity and creates queue pair
3. **Stream Establishment**: Bidirectional gRPC stream created
4. **Resync**: Initial synchronization based on agent mode
5. **Event Processing**: Continuous bidirectional event exchange
6. **Graceful Shutdown**: Connection cleanup and queue removal

### Event Format

All synchronization data is exchanged using CloudEvents format:

```json
{
  "specversion": "1.0",
  "source": "agent-name" | "principal",
  "type": "io.argoproj.argocd-agent.event.*",
  "dataschema": "application" | "appproject" | "resource" | "resourceResync",
  "extensions": {
    "resourceid": "uuid",
    "eventid": "uuid"
  },
  "data": { /* event-specific payload */ }
}
```

## Event Types and Flow

### Core Event Types

The protocol defines several event types for different synchronization scenarios:

#### Resource Management Events

- **`create`**: Create new resource (managed mode only)
- **`update`**: Update resource configuration
- **`delete`**: Remove resource (managed mode only)
- **`spec-update`**: Update resource specification
- **`status-update`**: Update resource status

#### Synchronization Events

- **`request-synced-resource-list`**: Request list of managed resources
- **`response-synced-resource`**: Response with resource metadata
- **`request-update`**: Request latest version of specific resource
- **`request-resource-resync`**: Trigger full resync process

#### Control Events

- **`ping`** / **`pong`**: Keepalive mechanism
- **`processed`**: Event acknowledgment

### Event Flow Patterns

#### Managed Mode Flow

```
Principal                           Agent
    │                                │
    │─── create/update/delete ────►  │  (Configuration)
    │                                │
    │  ◄─── status-update ───────────│  (Status feedback)
    │                                │
    │─── request-resource-resync ──► │  (On principal restart)
    │                                │
    │  ◄─── request-synced-resource  │  (Agent sends inventory)
    │      -list                     │
    │                                │
    │─── response-synced-resource ──►│  (For each resource)
```

#### Autonomous Mode Flow

```
Principal                           Agent
    │                                │
    │  ◄─── create/update/delete ────│  (Configuration changes)
    │                                │
    │─── status-update ─────────────► │  (Status sync)
    │                                │
    │─── request-synced-resource ──► │  (On principal restart)
    │     -list                      │
    │                                │
    │  ◄─── response-synced-resource │  (For each resource)
    │                                │
    │─── request-update ────────────► │  (Request specific updates)
```

## Synchronization Modes

### Managed Mode

In managed mode, the **principal is the source of truth** for configuration:

**Characteristics:**

- Principal creates/updates/deletes resources
- Agent receives configuration and applies it locally
- Agent sends status updates back to principal
- Principal initiates resync after restarts

**Event Flow:**

1. Configuration changes made on principal
2. Principal sends `create`/`update`/`delete` events to agent
3. Agent applies changes to local Argo CD
4. Agent sends `status-update` events back to principal
5. Principal updates UI/API with current status

**Namespace Mapping:**

- **Namespace-based (default):** Applications on principal are placed in namespaces named after target agents. Example: Applications in namespace `production-cluster` sync to agent `production-cluster`.
- **Destination-based:** Applications use `spec.destination.name` on the principal to specify the target agent, allowing multiple namespaces per agent. Example: An application with `destination.name: production-cluster` syncs to agent `production-cluster` regardless of its namespace.

See [Agent Mapping Modes](agent-mapping.md) for detailed configuration.

### Autonomous Mode

In autonomous mode, the **agent is the source of truth** for configuration:

**Characteristics:**

- Agent creates/updates/deletes resources locally
- Principal receives configuration updates and mirrors them
- Principal acts as read-only observer with limited control capabilities
- Agent initiates resync after restarts

**Event Flow:**

1. Configuration changes made on agent (via Git, local API, etc.)
2. Agent detects changes via informers
3. Agent sends `create`/`update`/`delete` events to principal
4. Principal mirrors changes for UI/API visibility
5. Principal can still trigger sync/refresh operations

## Resync Process

### Purpose

The resync process ensures data consistency when either the principal or agent restarts and may have missed events.

### Resync Triggers

#### Agent Restart (Managed Mode)

1. Agent connects and is detected as needing resync
2. Principal sends `request-resource-resync` event
3. Agent responds with `request-synced-resource-list` containing checksum
4. Principal compares checksums and sends missing/updated resources

#### Principal Restart (Autonomous Mode)

1. Principal detects agent connection after restart
2. Principal sends `request-synced-resource-list` with its checksum
3. Agent compares checksums and sends `response-synced-resource` for each resource
4. Principal rebuilds its state from agent responses

#### Principal Restart (Managed Mode)

1. Principal detects agent connection after restart
2. Principal sends `request-resource-resync` event
3. Agent sends `request-synced-resource-list` with checksum
4. Principal validates and sends any needed updates

### Resync State Management

The principal maintains resync state to avoid redundant resync operations:

```go
type resyncStatus struct {
    mu      sync.RWMutex
    agents  map[string]bool  // tracks which agents have been resynced
}
```

## Checksum-based Sync Detection

### Resource Identification

Resources are uniquely identified using a composite key:

```go
type ResourceKey struct {
    Name      string  // Resource name
    Namespace string  // Resource namespace  
    Kind      string  // Resource type (Application, AppProject)
    UID       string  // Source UID for tracking
}
```

### Checksum Calculation

Checksums are calculated from resource keys to efficiently detect synchronization drift:

```go
func (r *Resources) Checksum() []byte {
    resources := make([]string, 0, len(r.resources))
    for res := range r.resources {
        // Namespace omitted for cross-cluster compatibility
        resources = append(resources, fmt.Sprintf("%s/%s/%s", 
            res.Kind, res.Name, res.UID))
    }
    
    sort.Strings(resources)  // Ensure deterministic order
    hash := sha256.Sum256([]byte(strings.Join(resources, "")))
    return hash[:]
}
```

### Sync Detection Flow

1. **Checksum Exchange**: Peer sends checksum of all managed resources
2. **Comparison**: Recipient compares with local checksum
3. **Delta Sync**: If different, recipient requests specific resource updates
4. **Resource-level Checksums**: Individual resources compared via spec checksums

### Resource-level Sync

For individual resources, spec checksums detect when updates are needed:

```go
type RequestUpdate struct {
    Name      string
    Namespace string
    UID       string
    Kind      string
    Checksum  []byte  // SHA256 of resource spec
}
```

## Queue Management

### Queue Architecture

Each agent connection has a dedicated queue pair:

```go
type QueuePair struct {
    sendQ workqueue.TypedRateLimitingInterface[*cloudevents.Event]
    recvQ workqueue.TypedRateLimitingInterface[*cloudevents.Event]
}
```

### Queue Operations

#### Send Queue (Principal → Agent)

- **Purpose**: Buffer outgoing events to agent
- **Processing**: FIFO order with rate limiting
- **Blocking**: Sender blocks if queue is full

#### Receive Queue (Agent → Principal) 

- **Purpose**: Buffer incoming events from agent
- **Processing**: Parallel processing with semaphore control
- **Ordering**: Per-queue ordering maintained via named locks

### Event Processing Pipeline

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│   Receive   │───►│    Queue    │───►│  Process    │
│   Event     │    │   Event     │    │   Event     │
└─────────────┘    └─────────────┘    └─────────────┘
                           │
                           ▼
                   ┌─────────────┐
                   │   Send      │
                   │  Response   │
                   └─────────────┘
```

### Queue Lifecycle

1. **Creation**: Queue pair created when agent connects
2. **Processing**: Continuous event processing while connected
3. **Cleanup**: Queues drained and removed on disconnect
4. **Persistence**: Events may be held during brief disconnections

## Error Handling and Reliability

### Connection Resilience

#### Reconnection Logic

- **Exponential Backoff**: Agent implements backoff strategy for reconnections
- **State Preservation**: In-flight events preserved across reconnections
- **Automatic Resume**: Processing resumes where it left off

#### Error Classifications

- **Temporary Errors**: Network issues, temporary unavailability
- **Permanent Errors**: Authentication failures, invalid configurations
- **Recoverable Errors**: Resource conflicts, validation failures

### Event Reliability

#### Delivery Guarantees

- **At-least-once delivery**: Events may be processed multiple times
- **Ordering guarantees**: Per-queue FIFO ordering maintained
- **Idempotency**: Event handlers designed to be idempotent

#### Failure Scenarios

##### Agent Disconnection

1. Send queue preserves pending events
2. Receive queue continues processing
3. On reconnection, resync process handles missed events

##### Principal Restart  

1. All agent connections dropped
2. Agents automatically reconnect
3. Resync process rebuilds principal state

##### Network Partitions

1. Agents operate autonomously during partition
2. Resync resolves conflicts when connectivity restored
3. Checksums detect and resolve data drift

## Monitoring and Observability

### Metrics

- **Connection metrics**: Active connections, connection duration
- **Event metrics**: Events sent/received, processing latency
- **Error metrics**: Failed events, reconnection attempts
- **Queue metrics**: Queue depth, processing rate

### Logging

- **Structured logging**: JSON format with contextual fields
- **Trace IDs**: Event correlation across components
- **Debug levels**: Configurable verbosity for troubleshooting

## Performance Characteristics

### Scalability

- **Concurrent agents**: Principal can handle hundreds of simultaneous agents
- **Event throughput**: Thousands of events per second per agent
- **Memory usage**: Bounded by queue sizes and connection count

### Latency

- **Event propagation**: Sub-second latency for most events
- **Resync duration**: Proportional to resource count and network latency
- **Connection establishment**: Typically < 5 seconds including auth

### Tuning Parameters

- **Queue sizes**: Configurable per-queue limits
- **Processing concurrency**: Adjustable semaphore limits
- **Connection timeouts**: Configurable keepalive and timeout settings 