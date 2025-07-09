package redisproxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/principal/tracker"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// RedisProxy proxies traffic from principal Argo CD, to either, a) agent Argo CD redis or b) principal Argo CD redis, depending on the type and content of message.
type RedisProxy struct {

	// EventIDTracker maintains a channel that can be used to send/receive synchronous events; that is, cases where we are sending a specific sync message and expecting a specific sync response. The request/response both share a single event id.
	EventIDTracker *tracker.Tracker

	// ConnectionIDTracker maintains a channel that can be used to send/receive events to be handled by a specific Argo CD connection
	// - This is useful for asynchronous communication events, such as subscribe notifications
	ConnectionIDTracker *tracker.Tracker

	// sendSynchronousMessageToAgentFn is called to send a message (and wait for a response) to remote agent via principal's machinery
	// - should not be used for asynchronous messages.
	// - currently the main implementation is: 'sendSynchronousRedisMessageToAgent'
	sendSynchronousMessageToAgentFn sendSynchronousMessageToAgentFuncType

	// listenAddress is the address principal redis proxy will listen on
	listenAddress string

	// principalRedisAddress is the address of the principal redis server
	principalRedisAddress string

	// listener is the listener for the redis proxy
	listener net.Listener
}

const (
	constInternalNotify = "internal-notify"
)

// sendSynchronousMessageToAgentFuncType is the function signature used to send a message (and wait for a response) to remote agent via principal's machinery
type sendSynchronousMessageToAgentFuncType func(agentName string, connectionUUID string, body event.RedisCommandBody) *event.RedisResponseBody

func New(listenAddress string, principalRedisAddress string, sendSyncMessageToAgentFuncParam sendSynchronousMessageToAgentFuncType) *RedisProxy {

	res := &RedisProxy{
		sendSynchronousMessageToAgentFn: sendSyncMessageToAgentFuncParam,
		EventIDTracker:                  tracker.New(),
		ConnectionIDTracker:             tracker.New(),
		principalRedisAddress:           principalRedisAddress,
		listenAddress:                   listenAddress,
	}

	return res
}

// Start listening on redis proxy port, and handling connections
func (rp *RedisProxy) Start() error {

	l, err := net.Listen("tcp", rp.listenAddress)
	if err != nil {
		log().WithError(err).Error("error occurred on listening to addr: " + rp.listenAddress)
		return err
	}
	rp.listener = l

	// Start server and connection handler
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				log().WithError(err).Error("error occurred on accepting connection")
				return
			}
			go rp.handleConnection(conn)
		}
	}()

	log().Infof("Redis proxy started on %s", rp.listenAddress)

	return nil
}

// Stop listening on redis proxy port, and close server port listener
func (rp *RedisProxy) Stop() error {
	if rp.listener != nil {
		return rp.listener.Close()
	}
	return nil
}

// handleConnection is passed a TCP-IP socket connection from a redis client (e.g. Argo CD server). handleConnection initializes the connection and passes it to the message loop function.
// - This functions runs for the lifecycle of the connection.
func (rp *RedisProxy) handleConnection(fromArgoCDConn net.Conn) {

	defer fromArgoCDConn.Close()

	connUUID := uuid.New().String()

	logCtx := log().WithField("function", "redisFxn")
	logCtx = logCtx.WithField("connUUID", connUUID)

	redisConn, err := establishConnectionToPrincipalRedis(rp.principalRedisAddress, logCtx)
	if err != nil {
		logCtx.WithError(err).Error("unable to connect to principal redis")
		return
	}
	defer redisConn.Close()

	argoCDReader := bufio.NewReader(fromArgoCDConn) // input from argo cd

	// All writing to Argo CD socket should be through this object. This is required as multiple go-routines may write to this socket.
	argocdWriter := &argoCDRedisWriterInternal{
		fromArgoCDWrite: bufio.NewWriter(fromArgoCDConn),
		argoCDWriteLock: &sync.Mutex{},
	}

	redisReader := bufio.NewReader(redisConn) // input from principal redis
	redisWriter := bufio.NewWriter(redisConn) // output to principal redis

	endpointMessageChannel := startArgoCDRedisEndpointReader(logCtx, argoCDReader)

	connectionCtx, connectionCancelFn := context.WithCancel(context.Background())
	defer func() {
		logCtx.Trace("cancelling connection context")
		connectionCancelFn()
	}()

	go func() {
		// Any traffic sent from principal redis will be automatically forwarded back to principal argocd, without any modification.
		// - As of this writing, there is no need to proxy/MITM any traffic in the direction of (principal redis) -> (principal argo cd)
		if err := forwardTrafficSimple("r->a", redisReader, argocdWriter, logCtx); err != nil {

			if strings.Contains(err.Error(), "use of closed network connection") {
				logCtx.WithError(err).Trace("forwardTraffic exited due to closed network connection, this is usually expected behaviour.")
			} else {
				logCtx.WithError(err).Error("traffic forwarder returned unexpected error")
			}

			return
		}
	}()

	connState := &connectionState{
		connectionCtx:               connectionCtx,
		connectionCancelFn:          connectionCancelFn,
		connUUID:                    connUUID,
		connectionUUIDEventsChannel: nil,
		pingRoutineStarted:          map[string]bool{},
	}

	rp.handleConnectionMessageLoop(connState, endpointMessageChannel, argocdWriter, redisWriter, logCtx)

}

// connectionState is the state we track for a specific TCP-IP socket connection from Argo CD. The lifecycle of this struct (and related goroutines/functions) is equivalent to the lifecycle of that TCP-IP socket.
type connectionState struct {

	// connectionCtx should be cancelled in order to cause all connection-related go-routines to complete
	connectionCtx context.Context

	// connectionCancelFn is the cancel function for above
	connectionCancelFn context.CancelFunc

	// connUUID is the immutable identifier for this specific TCP/IP socket connection from Argo CD <-> agent redis proxy
	connUUID string

	// connectionUUIDEventsChannel is the channel that is responsible for reading asynchronous redis subscription responses from agents, for this specific connection (the connection identified by connUUID)
	connectionUUIDEventsChannel chan *cloudevents.Event

	// pingRoutineStarted indicates whether the go routine that sends 'ping' messages to agents has been started for a specific agent (name)
	// - This is used to ensure disconnected TCP-IP connections are closed on both sides (principal/agent)
	// - map key is 'agent name', value is not used.
	pingRoutineStarted map[string]bool
}

// handleConnectionMessageLoop is passed an initialized connection from handleConenction
// - This functions runs for the lifecycle of the connection.
func (rp *RedisProxy) handleConnectionMessageLoop(connState *connectionState, endpointMessageChannel chan parsedRedisCommand, argocdWriter argoCDRedisWriter, redisWriter *bufio.Writer, logCtx *logrus.Entry) {

	defer logCtx.Debug("handleConnectionMessageLoop has exited")

	for {

		// readNextMessageFromArgoCDOrInternal will read from two channels:
		// - one channel reading parsed redis messages from Argo CD
		// - one channel reading from subscribe notifications, which receives notify (push) messages from agents

		parsedRedisCommandVal := readNextMessageFromArgoCDOrInternal(connState, endpointMessageChannel, logCtx)

		if parsedRedisCommandVal.connContextCancelled {
			logCtx.Debug("exit due to cancelled context reported by readNextMessageFromArgoCDOrInternal")
			return
		}

		if parsedRedisCommandVal.err != nil {

			if strings.HasSuffix(parsedRedisCommandVal.err.Error(), "EOF") {
				logCtx.WithError(parsedRedisCommandVal.err).Debug("EOF error from readNextMessage, likely expected due to closed connection")
			} else {
				logCtx.WithError(parsedRedisCommandVal.err).Error("unexpected error from readNextMessage")
			}

			return
		}

		logCtx.Tracef("processing redis command: %s", parsedRedisCommandVal.generateParsedCommandDebugString())

		if len(parsedRedisCommandVal.internalMsg) == 2 {

			// Handle subscription notify (push)
			if err := handleInternalNotify(parsedRedisCommandVal.internalMsg, argocdWriter, logCtx); err != nil {
				logCtx.WithError(err).Error("exit due to unable to handle subscription notify command")
				return
			}
		}

		parsedReceived := parsedRedisCommandVal.parsedReceived

		forwardRawCommandToPrincipalRedis := true // whether to send the command to principal redis: false if the command has already been processed, true otherwise.

		// If we received a 'subscribe' message from argo cd, in the exact expected format
		if len(parsedReceived) == 2 && parsedReceived[0] == "subscribe" {

			// Example command:
			// 'subscribe' 'app|managed-resources|agent-managed_my-app|1.8.3'
			// - format is: subscribe "(key to subscribe to)""
			// - 'my-app' is name of Argo CD Application
			// - 'agent-managed' is the namespace of 'my-app' Application (and 'agent-managed' also correspond to Argo CD agent on another cluster)

			channelName := parsedReceived[1]

			agentName, err := extractAgentNameFromRedisCommandKey(channelName, logCtx)
			if err != nil {
				logCtx.WithError(err).Error("exit due to unable to get agent name: " + channelName)
				return
			}

			if agentName != "" { // We're only interested in handling subscribes that need to be forwarded to an agent redis

				forwardRawCommandToPrincipalRedis = false

				if err := rp.handleAgentSubscribe(connState, channelName, agentName, argocdWriter, logCtx); err != nil {
					logCtx.WithError(err).Error("exit due to unable to handle agent subscribe")
					return
				}
			}
		}

		// If we received a 'get' message from Argo CD...
		if len(parsedReceived) == 2 && parsedReceived[0] == "get" {

			// Determine if the command should be forwarded to agent (and which agent)
			key := parsedReceived[1]

			agentName, err := extractAgentNameFromRedisCommandKey(key, logCtx)
			if err != nil {
				logCtx.WithError(err).Error("exit due to unable to get agent name: " + key)
				return
			}

			if agentName != "" { // We're only interested in forwarding get commands that are destined for agent redis

				// agentName will be the agent namespace on principal cluster, so forward it to the corresponding argocd-agent instance on agent cluster

				forwardRawCommandToPrincipalRedis = false

				if err := rp.handleAgentGet(connState, key, argocdWriter, agentName, logCtx); err != nil {
					logCtx.WithError(err).Error("exit due to unable to handle agent get")
					return
				}
			}

		}

		// If the command we received was not 'get', 'subscribe', or 'internal-notify' (or if the 'get'/'subscribe' is known be principal-only), then just forward it directly to principal redis without modification.
		if forwardRawCommandToPrincipalRedis {
			rawReceived := parsedRedisCommandVal.rawReceived

			for _, raw := range rawReceived {
				if _, err := redisWriter.Write(([]byte)(raw + "\r\n")); err != nil {
					logCtx.WithError(err).Error("exit due to write error:", err.Error())
					return
				}
			}
			if err := redisWriter.Flush(); err != nil {
				logCtx.WithError(err).Error("exit due to flush error:", err.Error())
				return
			}
		}
	}
}

// handleInternalNotify	handle subscription notify (push) from agent
func handleInternalNotify(vals []string, argocdWriter argoCDRedisWriter, logCtx *logrus.Entry) error {

	if len(vals) != 2 {
		return fmt.Errorf("unexpected internal message command format: %v", vals)
	}

	// Example command:
	// internal-notify "(resp.Body.PushFromSubscribe.ChannelName)"

	if vals[0] != constInternalNotify {
		return fmt.Errorf("unexpected internal message command: %s", vals[0])
	}

	// We only support nil message subscription messages for now
	channelName := vals[1]
	channelNameBytes := ([]byte)(vals[1])

	if channelName == "" {
		return fmt.Errorf("channel name is empty")
	}

	logCtx = logCtx.WithField("channelName", channelName)

	// Example response that we need to send to principal Argo CD redis:
	// >3
	// $7
	// message  # 'message' type
	// $49  # length of channel name in bytes
	// app|managed-resources|agent-managed_my-app|1.8.3  # channel name
	// $0  # no body data to the notify response
	// (With each line ending with \r\n)

	msg := fmt.Appendf(nil, ">3\r\n$7\r\nmessage\r\n$%d\r\n%s\r\n$0\r\n\r\n", len(channelNameBytes), channelName)

	logCtx.Tracef("Wrote notify response to argo cd: '%s'", string(msg))

	if err := argocdWriter.writeToArgoCDRedisSocket(logCtx, msg); err != nil {
		logCtx.WithError(err).Error("unable to write response in handleInternalNotify")
		return fmt.Errorf("unable to write response")
	}

	return nil
}

// handleAgentGet processes a redis get message that is destinated for a specific agent
func (rp *RedisProxy) handleAgentGet(connState *connectionState, key string, argocdWriter argoCDRedisWriter, agentName string, logCtx *logrus.Entry) error {

	rp.beginPingGoRoutineIfNeeded(connState, agentName, logCtx)

	// Example of a redis get command request/response:
	// (A->R) Wrote: *2
	// (A->R) Wrote: $3
	// (A->R) Wrote: get  # 'get ' type
	// (A->R) Wrote: $52  # get key is 52 bytes
	// (A->R) Wrote: cluster|info|https://kubernetes.default.svc|1.8.3.gz  # get key

	// (R->A) Read and wrote: $177  # get response is 177 bytes
	// (R->A) Read and wrote: (... binary data ...) # get resposne

	logCtx = logCtx.WithField("getKey", key)

	cmdBody := event.RedisCommandBody{
		Get: &event.RedisCommandBodyGet{
			Key: key,
		},
	}

	agentName, err := extractAgentNameFromRedisCommandKey(key, logCtx)
	if err != nil {
		logCtx.WithError(err).Error("unable to get agent name: " + key)
		return err
	}

	logCtx.Trace("sending get command to agent")

	// This function will forward message to agent and await a response
	response := rp.sendSynchronousMessageToAgentFn(agentName, connState.connUUID, cmdBody)
	if response == nil {
		logCtx.Error("no response from agent")
		return fmt.Errorf("unexpected nil in get response")
	}

	if response.Get == nil {
		logCtx.Error("received a redis response from agent, but get body was nil")
		return fmt.Errorf("no get response in response command")
	}

	if response.Get.Error != "" {
		logCtx.Error("received a redis response from agent, but an error occurred: " + response.Get.Error)
		return fmt.Errorf("unexpected error: %s", response.Get.Error)

	} else if !response.Get.CacheHit {

		// Cache miss: no data for that key in agent redis

		response := "-\r\n" // Cache miss is a simple redis null

		logCtx.Tracef("Wrote GET response to argo cd: '%s'", response)

		if err := argocdWriter.writeToArgoCDRedisSocket(logCtx, ([]byte)(response)); err != nil {
			return err
		}

		return nil

	} else {

		// Cache hit: there was data for that key in agent redis, so forward back to principal Argo CD

		// We sanitize the log string because it may contain confidential data, which we don't want to log.
		logCtx.Tracef("Wrote GET response to argo cd: '%s' '%s'", key, sanitizeStringIfNonASCII(string(response.Get.Bytes)))

		// Get response format is as described above
		if err := argocdWriter.writeToArgoCDRedisSocket(logCtx, fmt.Appendf(nil, "$%d\r\n%s\r\n", len(response.Get.Bytes), response.Get.Bytes)); err != nil {
			return err
		}

		return nil
	}

}

// handleAgentSubscribe handles a subscription request from Argo CD to be forwarded to agent redis
func (rp *RedisProxy) handleAgentSubscribe(connState *connectionState, channelName string, agentName string, argoCDWrite argoCDRedisWriter, logCtx *logrus.Entry) error {

	rp.beginPingGoRoutineIfNeeded(connState, agentName, logCtx)

	// Create the channel and start the goroutine for the channel that is responsible for reading asynchronous redis subscription responses from agents
	if connState.connectionUUIDEventsChannel == nil {

		logCtx.Trace("begun tracking by connection uuid")
		connState.connectionUUIDEventsChannel = make(chan *cloudevents.Event)
		subscriptionChannel, err := rp.ConnectionIDTracker.Track(connState.connUUID, agentName)
		if err != nil {
			logCtx.WithError(err).Error("Unable to track by connection uuid")
			return fmt.Errorf("unable to track by connection UUID")
		}

		// Forward all events from 'subscriptionChannel' to 'connectionUUIDEventsChannel'
		go func() {
			defer close(connState.connectionUUIDEventsChannel)

			for {
				select {
				case <-connState.connectionCtx.Done():
					logCtx.Trace("cancelling connectionUUIDEventsChannel forwarding due to context cancelled")
					return
				case event := <-subscriptionChannel:
					logCtx.Trace("received redis subscription event from agent, forwarding to connection channel")
					connState.connectionUUIDEventsChannel <- event
				}
			}

		}()
	}

	// Send synchronous subscribe message to begin the subscription.
	cmdBody := event.RedisCommandBody{
		Subscribe: &event.RedisCommandBodySubscribe{
			ChannelName: channelName,
		},
	}

	// This function will wait for a response
	response := rp.sendSynchronousMessageToAgentFn(agentName, connState.connUUID, cmdBody)
	if response == nil {
		logCtx.Error("unexpected nil in get response")
		return fmt.Errorf("unexpected nil in get response")
	}

	// Example response to subscribe, to send back to Argo CD:
	// >3
	// $9
	// subscribe # 'subscribe' type message
	// $49  # 49 bytes in channel name
	// app|managed-resources|agent-managed_my-app|1.8.3  # channel name
	// :1

	// Send acknowledgment of subscribe back to Argo CD
	msgToSend := fmt.Appendf(nil, ">3\r\n$9\r\nsubscribe\r\n$%d\r\n%s\r\n:1\r\n", len(([]byte)(channelName)), channelName)

	logCtx.Tracef("(r->a) Wrote SUBSCRIBE response to argo cd: '%s': %s", channelName, msgToSend)
	if err := argoCDWrite.writeToArgoCDRedisSocket(logCtx, msgToSend); err != nil {
		return err
	}

	return nil
}

// beginPingGoRoutineIfNeeded starts a go routine that sends 'ping' messages to the agent for as long as the redis connection is active.
// - Only 1 ping go routine is started per agent (name)
// - This ensures that if principal or agent are restarted, that all redis connections on the other side are closed.
func (rp *RedisProxy) beginPingGoRoutineIfNeeded(connState *connectionState, agentName string, logCtx *logrus.Entry) {

	_, exists := connState.pingRoutineStarted[agentName]
	if exists {
		// Already started, so no-op
		return
	}

	connState.pingRoutineStarted[agentName] = true

	// Send 'ping' to the agent for as long as the redis connection is active.
	go func() {

		logCtx = logCtx.WithField("agent-name", agentName)

		logCtx.Debug("Beginning ping thread")
		for {
			ticker := time.NewTicker(1 * time.Minute)

			select {
			case <-ticker.C:
				logCtx.Trace("sending ping on ping thread")
				cmdBody := event.RedisCommandBody{
					Ping: &event.RedisCommandBodyPing{},
				}

				// sendSynchronousMessageToAgentFn waits up to X seconds for a respond to the message, and returns nil response if no response received.
				response := rp.sendSynchronousMessageToAgentFn(agentName, connState.connUUID, cmdBody)
				if response == nil {
					logCtx.Error("unexpected nil in response in ping thread, cancelling connection context")
					connState.connectionCancelFn()
					return
				}

			case <-connState.connectionCtx.Done():
				logCtx.Debug("ping thread terminated")
				return
			}
		}

	}()

}

// readNextMessageFromArgoCDOrInternal reads the next message from either the argo cd TCP-IP socket or the internal channel (used for subscription notify)
func readNextMessageFromArgoCDOrInternal(connState *connectionState, receiverChan chan parsedRedisCommand, logCtx *logrus.Entry) parsedRedisCommand {

	select {

	case <-connState.connectionCtx.Done(): // Stop reading if the connection context is cancelled
		return parsedRedisCommand{
			connContextCancelled: true,
		}

	// Read messages from argo cd TCP-IP socket
	case rcMessage, ok := <-receiverChan:

		if !ok {
			logCtx.Debug("receiver channel has closed")
			return parsedRedisCommand{connContextCancelled: true}
		}

		return rcMessage

	// Read internal (subscribe notify) messages sent to us by an agent
	case channelMessage := <-connState.connectionUUIDEventsChannel:

		// Get the resource out of the event
		resp := &event.RedisResponse{}

		err := channelMessage.DataAs(resp)
		if err != nil {
			logCtx.WithError(err).Error("Could not get data from event")
			return parsedRedisCommand{err: err}
		}
		return parsedRedisCommand{internalMsg: []string{constInternalNotify, resp.Body.PushFromSubscribe.ChannelName}}
	}
}

// generateParsedCommandDebugString is debug utility function
func (prc *parsedRedisCommand) generateParsedCommandDebugString() string {

	if prc.err != nil {
		return fmt.Sprintf("error occurred: %v", prc.err)
	}

	if len(prc.internalMsg) > 0 {
		return fmt.Sprintf("internal message: %v", prc.internalMsg)
	}

	res := "(a->r) "
	for _, str := range prc.parsedReceived {
		res += fmt.Sprintf("'%s' | ", sanitizeStringIfNonASCII(str))
	}

	return res

}

// sanitizeStringIfNonASCII: if the string is non-ascii (likely because it includes binary data), then only return the number of bytes of the string rather than the string itself
// - This is not done for security reasons; it is only to avoid dumping binary data to logs.
func sanitizeStringIfNonASCII(str string) string {
	containsNonASCII := len(str) != utf8.RuneCountInString(str)

	var res string

	if !containsNonASCII {
		res = str
	} else {
		res = fmt.Sprintf("( %d bytes )", len(str))
	}

	return res

}

// parsedRedisCommand is parsed commands (messages) from redis socket
type parsedRedisCommand struct {
	// err is non-nil if an error occurred during parsing
	err error

	// parseReceived is the parsed version of the redis message
	// example: ["ping"]
	parsedReceived []string

	// rawReceived is the raw, unparsed version of the redis message, as transmitted on wire
	// example: ["*1", "$4", "ping"]
	rawReceived []string

	// This struct is also used to communicate internal messages via this field: as of this writing, the only supported message that will be written to this field is:
	// internal-notify (subscription channel)
	internalMsg []string

	// connContextCancelled indicated whether the connection context was cancelled (indicating that the underlying connection has been closed, and so no further processing is required)
	connContextCancelled bool
}

// forwardTrafficSimple reads from 'r' and writes to 'argocdWriter'. Traffic is MITMed so that it can be output via debug.
// - 'simple' in the sense that we read/write with any modification to that data.
func forwardTrafficSimple(debugStr string, r *bufio.Reader, argocdWriter *argoCDRedisWriterInternal, logCtx *logrus.Entry) error {

	// Maintains a record of bytes forwarded, so we can output via debug log
	debugBuf := bytes.NewBuffer(make([]byte, 0))
	debugWrite := bufio.NewWriter(debugBuf)
	debugRead := bufio.NewReader(debugBuf)

	buf := make([]byte, 4096)
	for {

		// Read from input
		n, err := r.Read(buf)
		if err != nil {
			// Log as trace as this is likely as simple closed connection
			logCtx.WithError(err).Trace("unable to read from connection " + debugStr)
			return err
		}

		// Write to output
		if err := argocdWriter.writeToArgoCDRedisSocket(logCtx, buf[0:n]); err != nil {
			// Log as trace as this is likely as simple closed connection
			logCtx.WithError(err).Trace("unable to write to connection " + debugStr)
			return err
		}

		// Write to our debug buffer
		if _, err := debugWrite.Write(buf[0:n]); err != nil {
			logCtx.WithError(err).Debug("error returned by debugWrite Write " + debugStr)
		}
		if err := debugWrite.Flush(); err != nil {
			logCtx.WithError(err).Debug("error returned by debugWrite Flush " + debugStr)
		}

		// Write any (read) strings to debug log
		for {

			writtenStr, err := debugRead.ReadString('\n')
			if err != nil {
				if err != io.EOF { // EOF means no \n was found, not that the connection has closed (that is handled elsewhere)
					logCtx.WithError(err).Warn("unexpected error from redis debug output")
				}
				break
			}

			logCtx.Tracef("%s wrote '%d bytes'", debugStr, len(writtenStr))
			// replace the above with this to output the actual text:
			// logCtx.Tracef("%s wrote '%s'", debugStr, sanitizeString(writtenStr))
			// This line is commented out by default to avoid logging confidential data to logs

		}
	}
}

// argoCDRedisWriter is wrapper over argoCDRedisWriterInternal (primarily to avoid writing to internal fields, and to allow mocking)
type argoCDRedisWriter interface {
	writeToArgoCDRedisSocket(logCtx *logrus.Entry, bytes []byte) error
}

// argoCDRedisWriterInternal is a simple wrapper around argo cd redis socket, which ensures that lock is owned while writing to argo cd server.
type argoCDRedisWriterInternal struct {

	// Use 'writeToArgoCDRedisSocket' function: these internal fields should not be called directly.

	fromArgoCDWrite *bufio.Writer
	argoCDWriteLock *sync.Mutex
}

// writeToArgoCDRedisSocket writes data back to Argo CD while owning mutex
func (are *argoCDRedisWriterInternal) writeToArgoCDRedisSocket(logCtx *logrus.Entry, bytes []byte) error {

	are.argoCDWriteLock.Lock()
	defer are.argoCDWriteLock.Unlock()

	_, err := are.fromArgoCDWrite.Write(bytes)
	if err != nil {
		log().WithError(err).Error("unable to write to Argo CD Redis socket")
		return err
	}

	if err := are.fromArgoCDWrite.Flush(); err != nil {
		logCtx.WithError(err).Info("unable to flush to Argo CD Redis socket")
		return err
	}

	return nil
}

// establishConnectionToPrincipalRedis establishes a simple TCP-IP socket connection to principal's redis. (That is, we don't use go-redis client)
func establishConnectionToPrincipalRedis(principalRedisAddress string, logCtx *logrus.Entry) (*net.TCPConn, error) {

	var redisConn *net.TCPConn

	addr, err := net.ResolveTCPAddr("tcp", principalRedisAddress)
	if err != nil {
		logCtx.WithError(err).WithField("redisAddress", principalRedisAddress).Error("Resolution error")
		return nil, fmt.Errorf("unable to resolve address: %w", err)
	}

	// Dial the resolved address
	redisConn, err = net.DialTCP("tcp", nil, addr)
	if err != nil {
		logCtx.WithError(err).WithField("redisAddress", principalRedisAddress).Error("Connection error")
		return nil, fmt.Errorf("unable to connect to redis '%s': %w", principalRedisAddress, err)
	}

	return redisConn, nil
}

// Extract agent name from the key field of 'get' or 'subscribe' redis commands
// If found, return name. If not found, return "" (with nil err).
// If a field had an unexpected format, and error is returned.
//
// For example:
// - If the key is 'app|managed-resources|agent-managed_my-app|1.8.3'
// - The extracted agent name will be 'agent-managed'
//   - This value comes from the third field, before the '_' character
func extractAgentNameFromRedisCommandKey(redisKey string, logCtx *logrus.Entry) (string, error) {

	// We only recognize agent names from these keys
	if !strings.HasPrefix(redisKey, "app|managed-resources") && !strings.HasPrefix(redisKey, "app|resources-tree") {

		if redisKey == "new-revoked-token" {
			// 'new-revoked-token' is forwarded to principal
			return "", nil
		}

		if strings.HasPrefix(redisKey, "mfst|") {
			// mfst| is forwarded to principal, but I think this may not be a permanent behaviour.
			// Example key:
			// - mfst|annotation:app.kubernetes.io/instance|agent-managed_my-app|(revision id)|guestbook|3093507789|1.8.3.gz
			logCtx.Info("redirecting mfst| redis key to principal redis")
			return "", nil
		}

		logCtx.Errorf("Unexpected redis key seen: '%s'. Redirecting to principal by default.", redisKey)

		return "", nil
	}

	// Example keys:
	// * app|managed-resources|my-app|1.8.3
	// * app|resources-tree|my-app|1.8.3.gz
	// * app|managed-resources|agent-managed_my-app|1.8.3
	// * app|resources-tree|agent-managed_my-app|1.8.3.gz

	splitByPipe := strings.Split(redisKey, "|")
	if len(splitByPipe) != 4 {
		errMsg := fmt.Sprintf("unexpected number of fields in expected key: '%s'", redisKey)
		logCtx.Error(errMsg)
		return "", fmt.Errorf("%s", errMsg)
	}

	namespaceAndName := splitByPipe[2]

	// It is not possible to create a namespace name containing a '_' value, so this is a correct delimiter
	if !strings.Contains(namespaceAndName, "_") {
		errMsg := fmt.Sprintf("unexpected lack of '_' namespace/name separate: '%s'", redisKey)
		logCtx.Error(errMsg)
		return "", fmt.Errorf("%s", errMsg)
	}

	// namespace is the agent name
	res := namespaceAndName[0:strings.Index(namespaceAndName, "_")]

	return res, nil

}

func log() *logrus.Entry {
	return logrus.WithField("module", "redisProxy")
}
