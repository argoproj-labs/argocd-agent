# Transport Abstraction Design for argocd-agent

## Overview

현재 argocd-agent는 gRPC에 종속적인 구조로 되어 있어, 다양한 네트워크 환경에서의 유연성이 제한됩니다. 이 문서는 gRPC 종속성을 제거하고 다양한 transport를 지원하는 추상화 설계를 제안합니다.

### Current Issues
- **gRPC 종속성**: Application layer가 gRPC Protobuf에 직접 의존
- **제한된 네트워크 호환성**: HTTP/2 필수, 일부 proxy 환경에서 제한
- **확장성 부족**: 새로운 transport 추가 어려움

## Current Architecture (Before)

## Protocol Stack 변화

### Before (현재)
┌─────────────────────────────────────┐
│         Application Events          │  ← Create, Update, Delete, Status
├─────────────────────────────────────┤
│        CloudEvents Format           │  ← Event envelope with metadata
├─────────────────────────────────────┤
│         gRPC Streaming              │  ← Bidirectional streams (고정)
├─────────────────────────────────────┤
│        HTTP/2 (or HTTP/1+WS)        │  ← Transport layer (gRPC만 가능)
├─────────────────────────────────────┤
│           TLS + mTLS                │  ← Security layer
└─────────────────────────────────────┘


### After (Transport 추상화)
┌─────────────────────────────────────┐
│         Application Events          │  ← Create, Update, Delete, Status
├─────────────────────────────────────┤
│        CloudEvents Format           │  ← Event envelope with metadata
├─────────────────────────────────────┤
│      Transport Interface Layer      │  ← NEW: Connection + Stream interfaces
├─────────────────────────────────────┤
│  ┌─────────┬─────────┬─────────────┐ │
│  │  gRPC   │  HTTP   │   Kafka     │ │  ← Multiple transport options
│  │Streaming│ SSE+POST│ Pub/Sub     │ │
│  └─────────┴─────────┴─────────────┘ │
├─────────────────────────────────────┤
│ HTTP/2+WS │ HTTP/1.1 │ TCP/Binary   │  ← Transport-specific protocols
├─────────────────────────────────────┤
│           TLS + mTLS                │  ← Security layer (공통)
└─────────────────────────────────────┘


But, principal -> agent 방향의 bidirectional 은 유지합니다. (queue를 제외하고)

### Before (현재)
```
pkg/api/grpc/eventstreamapi/
├── eventstream.pb.go           ← Protobuf 메시지
├── eventstream_grpc.pb.go      ← gRPC 서비스
└── (gRPC만 지원)

pkg/client/
└── remote.go                   ← gRPC client만

agent/
├── connection.go               ← gRPC 직접 사용
└── agent.go                    ← *client.Remote 의존

principal/apis/eventstream/
└── eventstream.go              ← gRPC server 직접 구현
```

### After (Transport 추상화)
```
pkg/api/eventstreamapi/
├── interface.go                ← Connection, Stream interfaces
├── factory.go                  ← Transport factory
├── config.go                   ← Config types
├── grpc/                       ← gRPC transport
│   ├── eventstream.pb.go       ← (기존 파일 이동)
│   ├── eventstream_grpc.pb.go  ← (기존 파일 이동)
│   ├── connection.go           ← GRPCConnection 구현
│   ├── stream.go               ← GRPCStream 구현
│   └── websocket.go            ← WebSocket 지원
# we can 확장..
├── http/                       ← HTTP transport (NEW)
│   ├── connection.go           ← HTTPConnection 구현
│   ├── stream.go               ← HTTPStream 구현
│   └── server.go               ← HTTP server
└── kafka/                      ← Kafka transport (NEW)
    ├── connection.go           ← KafkaConnection 구현
    ├── stream.go               ← KafkaStream 구현
    └── server.go               ← Kafka server

pkg/client/
└── remote.go                   ← (기존 유지, gRPC transport에서 래핑)

agent/
├── connection.go               ← eventstreamapi.Connection 사용
└── agent.go                    ← eventstreamapi.Connection 의존

principal/apis/eventstream/
└── eventstream.go              ← eventstreamapi.Stream 사용
```

### 4. 각 Transport별 구현
- **gRPC**: 기존 pkg/client/remote.go 래핑
- **HTTP**: SSE(수신) + POST(송신) 조합
- **Kafka**: Producer(송신) + Consumer(수신) 조합

## 핵심 변화점

### 1. Interface Layer 추가
```
// pkg/api/eventstreamapi/interface.go
type Connection interface {
    Connect(ctx context.Context) error
    CreateStream(ctx context.Context) (Stream, error)
    Close() error
}

type Stream interface {
    Send(*cloudevents.Event) error
    Receive() (*cloudevents.Event, error)
    Close() error
}
```

### 2. Transport Factory
CLI 로 --transport를 정할 때 함께 생성 됩니다.
**현재 흐름:**
1. Agent 시작 → gRPC 직접 연결 → 스트림 생성 → 이벤트 송수신

**변경 후 흐름:**
1. Agent 시작 → Factory로 transport 선택 → 연결 → 스트림 생성 → 이벤트 송수신

```
// pkg/api/eventstreamapi/factory.go
func NewFactory() *Factory
func (f *Factory) Create(config Config) Connection

// 사용법
factory := eventstreamapi.NewFactory()
conn := factory.Create(eventstreamapi.Config{
    Type: "grpc",      // "grpc", "http", "kafka"
    Endpoint: "...",
})
```

### 3. Application Layer 변화
```
// Before
type Agent struct {
    remote *client.Remote  // gRPC 직접 의존
}

// After  
type Agent struct {
    connection eventstreamapi.Connection  // Transport 무관
}
```

### 사용 중인 Events 들 (managed, autonomous)

- **/internal/event/event.go**: 모든 이벤트 타입 상수 정의
- **/principal/event.go**: Principal의 이벤트 처리 로직 (모드별 필터링)
- **/agent/outbound.go**: Agent의 이벤트 생성 로직 (모드별 타입 선택)
- **/principal/apis/eventstream/eventstream.go**: 이벤트 스트림 처리 및 필터링

ping, pong, send 등..

## Principal ↔ Agent 통신 흐름 (Before/After)

#### 1단계: 연결 설정 (Connection Establishment)

#### Before (현재 gRPC 종속)
```go
// Agent 측
// /agent/connection.go
func (a *Agent) maintainConnection() error {
    if !a.IsConnected() {
        // gRPC 직접 연결
        err = a.remote.Connect(a.context, false)  // *client.Remote (gRPC)
        if err == nil {
            a.SetConnected(true)
        }
    }
}

// Principal 측  
// /principal/apis/eventstream/eventstream.go
func (s *Server) Subscribe(subs eventstreamapi.EventStream_SubscribeServer) error {
    // gRPC 서버가 직접 스트림 수신
    c, err := s.newClientConnection(subs.Context(), s.options.MaxStreamDuration)
}
```

#### After (Transport 추상화)
```go
// Agent 측
// /agent/connection.go
func (a *Agent) maintainConnection() error {
    if !a.IsConnected() {
        // Transport-agnostic 연결
        err = a.connection.Connect(a.context)  // eventstreamapi.Connection
        if err == nil {
            a.SetConnected(true)
        }
    }
}

// Principal 측
// /principal/apis/eventstream/eventstream.go
// stream data format이 변경되었습니다.
func (s *Server) Subscribe(stream eventstreamapi.Stream) error {
    // Transport-agnostic 스트림 수신
    c, err := s.newClientConnection(stream.Context(), s.options.MaxStreamDuration)
}
```

### 사용하는 Stream 객체의 변화


### 2단계: 스트림 생성 (Stream Creation)

#### Before (gRPC 스트림)
```go
// Agent 측
// /agent/connection.go
func (a *Agent) handleStreamEvents() error {
    conn := a.remote.Conn()  // gRPC connection
    client := eventstreamapi.NewEventStreamClient(conn)  // gRPC client
    stream, err := client.Subscribe(a.context)  // gRPC bidirectional stream
    
    a.eventWriter = event.NewEventWriter(stream)  // gRPC stream 직접 사용
}
```

#### After (Stream 인터페이스)
```go
// Agent 측
func (a *Agent) handleStreamEvents() error {
    stream, err := a.connection.CreateStream(a.context)  // eventstreamapi.Stream
    
    a.eventWriter = event.NewEventWriter(stream)  // Stream interface 사용
}
```

### 3단계: 이벤트 송신 (Event Sending)

#### Before (Protobuf 변환 + gRPC)
```go
// Agent → Principal
// /internal/event/event.go
func (ew *EventWriter) sendEvent(resID string) {
    // CloudEvent → Protobuf 변환 (Application layer에서)
    pev, err := format.ToProto(eventMsg.event)  // cloudevents.Event → pb.CloudEvent
    
    // gRPC 스트림으로 전송
    err = ew.target.Send(&eventstreamapi.Event{Event: pev})  // gRPC-specific
}

// streamWriter interface (gRPC 종속)
type streamWriter interface {
    Send(*eventstreamapi.Event) error  // Protobuf 메시지
    Context() context.Context
}
```

#### After (CloudEvent 직접 전송)
```go
// Agent → Principal  
func (ew *EventWriter) sendEvent(resID string) {
    // CloudEvent 직접 전송 (Protobuf 변환 제거)
    err := ew.target.Send(eventMsg.event)  // cloudevents.Event 직접 사용
}

// streamWriter interface (Transport-agnostic)
type streamWriter interface {
    Send(*cloudevents.Event) error  // CloudEvent 직접
    Context() context.Context
}

// Transport별 Protobuf 변환은 내부에서 처리
// pkg/api/eventstreamapi/grpc/stream.go
func (s *GRPCStream) Send(event *cloudevents.Event) error {
    pev, err := format.ToProto(event)  // gRPC transport에서만 변환
    return s.grpcStream.Send(&eventstreamapi.Event{Event: pev})
}
```

### 4단계: 이벤트 수신 (Event Receiving)

#### Before (gRPC + Protobuf 역변환)
```go
// Principal ← Agent
// /principal/apis/eventstream/eventstream.go
func (s *Server) recvFunc(c *client, subs eventstreamapi.EventStream_SubscribeServer) error {
    // gRPC에서 Protobuf 수신
    streamEvent, err := subs.Recv()  // *eventstreamapi.Event (Protobuf)
    
    // Protobuf → CloudEvent 변환 (Application layer에서)
    incomingEvent, err := format.FromProto(streamEvent.Event)  // pb.CloudEvent → cloudevents.Event
    
    // 수신 큐에 추가
    q.Add(incomingEvent)
}
```

#### After (CloudEvent 직접 수신)
```go
// Principal ← Agent
func (s *Server) recvFunc(c *client, stream eventstreamapi.Stream) error {
    // CloudEvent 직접 수신 (Protobuf 변환 제거)
    incomingEvent, err := stream.Receive()  // cloudevents.Event 직접 반환
    
    // 수신 큐에 추가
    q.Add(incomingEvent)
}

// Transport별 Protobuf 변환은 내부에서 처리
// pkg/api/eventstreamapi/grpc/stream.go
func (s *GRPCStream) Receive() (*cloudevents.Event, error) {
    streamEvent, err := s.grpcStream.Recv()  // gRPC에서 수신
    return format.FromProto(streamEvent.Event), err  // gRPC transport에서만 변환
}
```

### 5단계: 양방향 통신 루프 (Bidirectional Communication Loop)

#### Before (gRPC 고정)
```go
// Agent 측 - 송신/수신 루프
// /agent/connection.go
go func() {
    for a.IsConnected() {
        err = a.sender(stream)    // gRPC stream 직접 사용
        err = a.receiver(stream)  // gRPC stream 직접 사용
    }
}()

// Principal 측 - 송신/수신 루프  
// /principal/connection.go
go func() {
    for {
        err := s.recvFunc(c, subs)  // gRPC stream 직접 사용
        err := s.sendFunc(c, subs)  // gRPC stream 직접 사용
    }
}()
```

#### After (Transport 무관)
```go
// Agent 측 - 송신/수신 루프
// /agent/connection.go
go func() {
    for a.IsConnected() {
        err = a.sender(stream)    // eventstreamapi.Stream interface
        err = a.receiver(stream)  // eventstreamapi.Stream interface  
    }
}()

// Principal 측 - 송신/수신 루프
// /principal/connection.go
go func() {
    for {
        err := s.recvFunc(c, stream)  // eventstreamapi.Stream interface
        err := s.sendFunc(c, stream)  // eventstreamapi.Stream interface
    }
}()
```

## 핵심 변화 요약

**Before**: Application Layer → gRPC Protobuf → gRPC Stream → Network
**After**: Application Layer → CloudEvent → Transport Interface → Network

1. **Protobuf 변환 위치**: Application layer → Transport layer로 이동
2. **인터페이스 추상화**: gRPC-specific → Transport-agnostic interfaces  
3. **코드 재사용**: 기존 gRPC 로직 완전 보존하면서 새 transport 추가
4. **설정 기반 선택**: `--transport-type` 플래그로 runtime 선택

### CLI Usage
```bash
# Current
argocd-agent agent --enable-websocket=true

# After
argocd-agent agent --transport-type=grpc      # Default
argocd-agent agent --transport-type=grpc-ws   # WebSocket fallback
argocd-agent agent --transport-type=http      # HTTP transport
argocd-agent agent --transport-type=kafka     # Message router
```

## Conclusion

양방향 실시간이 왜 필요한걸까..