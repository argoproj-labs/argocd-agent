# Transport Abstraction Design for argocd-agent

## Meta

|||
|---------|------|
|**Title**|Transport Abstraction Design for argocd-agent|
|**Document status**|Work in Progress|
|**Document author**|@bianbbc87|
|**Implemented**|No|

## Abstract

Currently, argocd-agent has a structure dependent on gRPC, which limits flexibility in various network environments. This document proposes an abstraction design that removes gRPC dependency and supports various transports.

Through transport abstraction, argocd-agent can support various protocols such as gRPC, HTTP, and Kafka, enabling stable operation in diverse network environments including corporate networks, air-gapped environments, and edge deployments.

## Requirements
* Transport abstraction must maintain backward compatibility with existing gRPC implementation
* New transports (HTTP, Kafka) must support the same event semantics as gRPC

## Design

### Transport Flow
#### Before (Current)
┌─────────────────────────────────────┐
│         Application Events          │  ← Create, Update, Delete, Status
├─────────────────────────────────────┤
│        CloudEvents Format           │  ← Event envelope with metadata
├─────────────────────────────────────┤
│         gRPC Streaming              │  ← Bidirectional streams (fixed) (eventstream.go)
├─────────────────────────────────────┤
│        HTTP/2 (or HTTP/1+WS)        │  ← Transport layer (gRPC only) (listen.go -> listen.go)
├─────────────────────────────────────┤
│           TLS + mTLS                │  ← Security layer
└─────────────────────────────────────┘


#### After (Transport Abstraction)
┌─────────────────────────────────────┐
│         Application Events          │  ← Create, Update, Delete, Status
├─────────────────────────────────────┤
│        CloudEvents Format           │  ← Event envelope with metadata
├─────────────────────────────────────┤
│      Transport Interface Layer      │  ← NEW: Connection + Stream interfaces (listen.go)
├─────────────────────────────────────┤
│ ┌─────────┬─────────┬─────────────┐ │
│ │  gRPC   │  HTTP   │   Kafka     │ │  ← Multiple transport options (handler-*.go)
│ │Streaming│ SSE+POST│ Pub/Sub     │ │
│ └─────────┴─────────┴─────────────┘ │
├─────────────────────────────────────┤
│ HTTP/2+WS │ HTTP/1.1 │ TCP/Binary   │  ← Transport-specific protocols (remote-*.go)
├─────────────────────────────────────┤
│           TLS + mTLS                │  ← Security layer (common)
└─────────────────────────────────────┘

### Before Flow
Application Layer → gRPC Protobuf → gRPC Stream → HTTP/2+mTLS
(agent/principal)   (format conversion)     (fixed protocol)   (Network)

### After Flow
Application Layer → Factory(Config) → Transport Interface → CloudEvent → Multiple Transports
(agent/principal)   (config-based selection)    (Connection+Stream)   (common format)   (gRPC/HTTP/Kafka/Custom)

### Package Structure
### Before (Current)
```
pkg/api/grpc/eventstreamapi/
├── eventstream.pb.go           ← Protobuf messages
├── eventstream_grpc.pb.go      ← gRPC service
└── (gRPC only support)

pkg/client/
└── remote.go                   ← gRPC client only

agent/
├── connection.go               ← Direct gRPC usage (dependent components)
└── agent.go                    ← *client.Remote (dependent components)

principal/
└── listen.go ← TCP listener management, gRPC server creation and startup (calls eventstream.go)
└── server.go ← Principal overall server structure and configuration, server creation and initialization, server startup (calls listen.go)

principal/apis/eventstream/
└── eventstream.go              ← Communication with Agent (handles receive, send methods)
```

### After (Transport Abstraction)
```
pkg/api/eventstreamapi/
├── interface.go                ← Connection, Stream interfaces // Overall interface for Connection, stream (all eventstreamapi.~ imports from here)
├── factory.go                  ← Transport factory // Determines transport type at startup, initializes connection, stream objects
├── config.go                   ← Config types (actual config used by factory)
├── grpc/                       ← gRPC transport
│   ├── eventstream.pb.go       ← (moved from existing file)
│   ├── eventstream_grpc.pb.go  ← (moved from existing file)
│   ├── stream.go               ← GRPCStream implementation (need to wrap existing eventstream.pb.go, eventstream_grpc.pb.go as Stream object for interface calls)
# we can extend..
├── http/                       ← HTTP transport (NEW)
│   ├── stream.go               ← HTTPStream implementation
└── kafka/                      ← Kafka transport (NEW)
    ├── stream.go               ← KafkaStream implementation

pkg/client/
└── remote-grpc.go                   ← (maintain existing, wrapped by gRPC transport, also handles ws here)
└── remote-http.go                   ← http Connection implementation
└── remote-kafka.go                  ← kafka Connection implementation

agent/
├── connection.go               ← Uses eventstreamapi.Connection
└── agent.go                    ← Depends on eventstreamapi.Connection

principal/
├── server.go                          ← Add transportType struct variable here
├── listen.go                          ← Extended (start all Transport servers)
│   ├── serveGRPC()                    ← Existing
│   ├── serveHTTP()                    ← Newly added
│   └── serveKafka()                   ← Newly added
└── apis/eventstream/
    ├── handler_grpc.go                ← Just renamed from existing eventstream.go
    ├── handler_http.go                ← Newly added (HTTP)
    └── handler_kafka.go               ← Newly added (Kafka)
```

### Code Addition - Interface Implementation

#### pkg/api/eventstreamapi/config.go

- Structures transport settings received from CLI flags or configuration files

```go
type Config struct {
    Type     string                 // "grpc", "http", "kafka"
    Endpoint string                 // Connection address
    Options  map[string]interface{} // Transport-specific settings
}

// Transport type constants
const (
    TransportGRPC  = "grpc"
    TransportHTTP  = "http" 
    TransportKafka = "kafka"
)
```

#### pkg/api/eventstreamapi/interface.go

- Defines common contracts that all transports must implement

```
// connection interface
// pkg/client/remote-*.go are implementations
type Connection interface {
    Connect(ctx context.Context) error
    CreateStream(ctx context.Context) (Stream, error)
    Close() error
}

// stream interface
// pkg/api/eventstreamapi/*/stream.go are implementations
type Stream interface {
    Send(ctx context.Context, *cloudevents.Event) error
    Receive(ctx context.Context) (*cloudevents.Event, error)
    Close(ctx context.Context) error
}
```

#### pkg/api/eventstreamapi/factory.go

- Factory that creates appropriate transport implementations based on configuration

```
// pkg/api/eventstreamapi/factory.go
type Factory interface {
    CreateConnection(config Config) (Connection, error)
}

// ...
// implements example
func NewFactory() *Factory
func (f *Factory) Create(config Config) Connection {
    switch config.Type {
    case "grpc":
        remote := client.NewGRPCRemote(...)
        return &GRPCConnection{remote: remote}
    case "http":
        remote := client.NewHTTPRemote(...)
        return &HTTPConnection{remote: remote}
    case "kafka":
        remote := client.NewKafkaRemote(...)
        return &KafkaConnection{remote: remote}
    }
    ...
}

// Usage
factory := eventstreamapi.NewFactory()
conn := factory.Create(eventstreamapi.Config{
    Type: "grpc",      // "grpc", "http", "kafka"
    Endpoint: "...",
})
```

### Code Modification - Application Layer Changes
```
// Before
type Agent struct {
    remote *client.Remote  // Direct gRPC dependency
}

// After  
type Agent struct {
    connection eventstreamapi.Connection  // Transport agnostic
}
```
### Code Modification - listen, server Extension

#### principal/listen.go
```go
// principal/listen.go - Extended
import (
    "principal/apis/eventstream"  // Import all handlers
)

func (s *Server) Start(ctx context.Context, errch chan error) error {
    switch s.transportType {
    case "grpc":
        return s.serveGRPC(ctx, metrics, errch)
    case "http":
        return s.serveHTTP(ctx, metrics, errch)
    case "kafka":
        return s.serveKafka(ctx, metrics, errch)
    }
}

func (s *Server) serveHTTP(ctx context.Context, metrics *metrics.PrincipalMetrics, errch chan error) error {
   ...
}
...
```

##### listen.go serves as interface for implementation handler-*.go
```go

┌─────────────────┐
│  server.go      │ ← Top level (add transportType)
│ (Principal overall) │
└─────────────────┘
         │ owns + transportType
         ▼
┌─────────────────┐
│  listen.go      │ ← Network layer (Transport branching)
│ (Start all servers) │
└─────────────────┘
         │ creates & registers
         ├─────────────────┬─────────────────┐
         ▼                 ▼                 ▼
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│grpc_handler │  │http_handler │  │kafka_handler│ ← Business logic
│(existing eventstream)│ │(newly added)  │  │(newly added)  │
└─────────────┘  └─────────────┘  └─────────────┘
```

#### principal/server.go

- http, grpc already exist
- Extend server type for tcp

```go
type Server struct {
    transportType string  // Newly added
    // Existing fields...
}
```

### Transport Abstraction Before, After

#### 1. connect server

- Connection implementations are in `pkg/client/remote-*.go` path

##### Before (Current gRPC Dependency)
```go
// Agent side
// /agent/connection.go
func (a *Agent) maintainConnection() error {
    if !a.IsConnected() {
        // Direct gRPC connection
        err = a.remote.Connect(a.context, false)  // *client.Remote (gRPC)
        if err == nil {
            a.SetConnected(true)
        }
    }
}
```

##### After (Transport Abstraction)
```go
// Agent side
// /agent/connection.go
func (a *Agent) maintainConnection() error {
    if !a.IsConnected() {
        // Transport-agnostic connection
        err = a.connection.Connect(a.context)  // eventstreamapi.Connection, defined in interface.go
        if err == nil {
            a.SetConnected(true)
        }
    }
}
```


#### 2. create data stream

- Stream implementations are in `pkg/api/eventstreamapi/*/stream.go`

##### Before (gRPC Stream)
```go
// Agent side
// /agent/connection.go
func (a *Agent) handleStreamEvents() error {
    conn := a.remote.Conn()  // gRPC connection
    client := eventstreamapi.NewEventStreamClient(conn)  // gRPC client
    stream, err := client.Subscribe(a.context)  // gRPC bidirectional stream
    
    a.eventWriter = event.NewEventWriter(stream)  // Direct gRPC stream usage
}
```

##### After (Stream Interface)
```go
// Agent side
// /agent/connection.go
func (a *Agent) handleStreamEvents() error {
    stream, err := a.connection.CreateStream(a.context)  // eventstreamapi.Stream
    
    a.eventWriter = event.NewEventWriter(stream)  // Stream interface usage
}
```

#### 3. event api (server method)

- Stream implementations are in `principal/apis/eventstream/handler-*.go`

#### Principal ← Agent
##### Before (Protobuf Conversion + gRPC)
```go
// Agent → Principal
// /internal/event/event.go
func (ew *EventWriter) sendEvent(resID string) {
    // CloudEvent → Protobuf conversion (at Application layer)
    pev, err := format.ToProto(eventMsg.event)  // cloudevents.Event → pb.CloudEvent
    
    // Send via gRPC stream
    err = ew.target.Send(&eventstreamapi.Event{Event: pev})  // gRPC-specific
}

// streamWriter interface (gRPC dependent)
type streamWriter interface {
    Send(*eventstreamapi.Event) error  // Protobuf message
    Context() context.Context
}
```

##### After (Direct CloudEvent Transmission)
```go
// Agent → Principal  
func (ew *EventWriter) sendEvent(resID string) {
    // Direct CloudEvent transmission (remove Protobuf conversion)
    err := ew.target.Send(eventMsg.event)  // Direct cloudevents.Event usage
}

// streamWriter interface (Transport-agnostic)
type streamWriter interface {
    Send(*cloudevents.Event) error  // Direct CloudEvent
    Context() context.Context
}

// Transport-specific Protobuf conversion handled internally
// principal/apis/eventstream/handler-grpc.go
func (s *GRPCStream) Send(event *cloudevents.Event) error {
    pev, err := format.ToProto(event)  // Convert only in gRPC transport
    return s.grpcStream.Send(&eventstreamapi.Event{Event: pev})
}
```
#### Principal ← Agent
##### Before (gRPC + Protobuf Reverse Conversion)
```go
// Principal ← Agent
// /principal/apis/eventstream/eventstream.go
func (s *Server) recvFunc(c *client, subs eventstreamapi.EventStream_SubscribeServer) error {
    // Receive Protobuf from gRPC
    streamEvent, err := subs.Recv()  // *eventstreamapi.Event (Protobuf)
    
    // Protobuf → CloudEvent conversion (at Application layer)
    incomingEvent, err := format.FromProto(streamEvent.Event)  // pb.CloudEvent → cloudevents.Event
    
    // Add to receive queue
    q.Add(incomingEvent)
}
```

##### After (Direct CloudEvent Reception)
```go
// Principal ← Agent
func (s *Server) recvFunc(c *client, stream eventstreamapi.Stream) error {
    // Direct CloudEvent reception (remove Protobuf conversion)
    incomingEvent, err := stream.Receive()  // Direct cloudevents.Event return
    
    // Add to receive queue
    q.Add(incomingEvent)
}

// Transport-specific Protobuf conversion handled internally
// principal/apis/eventstream/handler-grpc.go
func (s *GRPCStream) Receive() (*cloudevents.Event, error) {
    streamEvent, err := s.grpcStream.Recv()  // Receive from gRPC
    return format.FromProto(streamEvent.Event), err  // Convert only in gRPC transport
}
```

### Define events methods in handler-*.go

- **/internal/event/event.go**: Define all event type constants
- **/principal/event.go**: Principal's event processing logic (mode-specific filtering)
- **/agent/outbound.go**: Agent's event generation logic (mode-specific type selection)
- **/principal/apis/eventstream/eventstream.go**: Event stream processing and filtering

ping, pong, send etc..

#### Bidirectional Communication Loop

#### Before (gRPC Fixed)
```go
// Agent side - send/receive loop
// /agent/connection.go
go func() {
    for a.IsConnected() {
        err = a.sender(stream)    // Direct gRPC stream usage
        err = a.receiver(stream)  // Direct gRPC stream usage
    }
}()

// Principal side - send/receive loop  
// /principal/connection.go
go func() {
    for {
        err := s.recvFunc(c, subs)  // Direct gRPC stream usage
        err := s.sendFunc(c, subs)  // Direct gRPC stream usage
    }
}()
```

#### After (Transport Agnostic)
```go
// Agent side - send/receive loop
// /agent/connection.go
go func() {
    for a.IsConnected() {
        err = a.sender(stream)    // eventstreamapi.Stream interface
        err = a.receiver(stream)  // eventstreamapi.Stream interface  
    }
}()

// Principal side - send/receive loop
// /principal/connection.go
go func() {
    for {
        err := s.recvFunc(c, stream)  // eventstreamapi.Stream interface
        err := s.sendFunc(c, stream)  // eventstreamapi.Stream interface
    }
}()
```
### CLI Usage Example
```bash
# Current
argocd-agent agent --enable-websocket=true

# After
argocd-agent agent --transport-type=grpc      # Default
argocd-agent agent --transport-type=grpc-ws   # WebSocket fallback
argocd-agent agent --transport-type=http      # HTTP transport
argocd-agent agent --transport-type=kafka     # Message router
```

### Required Changes

1. **Protobuf conversion location**: Move from Application layer → Transport layer
2. **Interface abstraction**: gRPC-specific → Transport-agnostic interfaces  
3. **Code reuse**: Completely preserve existing gRPC logic while adding new transports
4. **Configuration-based selection**: Runtime selection with `--transport-type` flag
5. **pkg/client/remote.go naming change**: `remote.go` -> `remote-grpc.go`
6. **/principal/apis/eventstream/eventstream.go naming change**: `eventstream.go` -> `handler-grpc.go`

### Extension Methods
#### Extensible Transport Areas
- **gRPC**: Wrap existing pkg/client/remote.go
- **HTTP**: SSE(receive) + POST(send) combination
- **Kafka**: Producer(send) + Consumer(receive) combination

#### Step-by-step Extension Method
1. refactor `pkg/api/eventstreamapi/config.go`: Add Transport constants
2. add `pkg/client/remote-*.go`: Add Connection implementations
3. add `pkg/api/eventstreamapi/*/stream.go`: Add Stream implementations
4. refactor `pkg/api/eventstreamapi/factory.go`: Add create connection for new cases
5. refactor: `principal/listen.go`: Define server type for new cases
6. add `principal/apis/eventstream/handler-*.go`: Add server handler for new types
