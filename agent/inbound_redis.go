// Copyright 2025 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	rediscache "github.com/go-redis/cache/v9"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// redisProxyMsgHandler manages redis connection state for agent
type redisProxyMsgHandler struct {
	redisAddress  string
	redisUsername string
	redisPassword string

	// argoCDRedisClient is the client API to agent redis
	argoCDRedisClient *redis.Client

	// argoCDRedisCache is the cache API to agent redis
	argoCDRedisCache *rediscache.Cache

	// connections maintains statistics about redis connections from principal
	connections *connectionEntries
}

// connectionEntries maintains statistics about redis connections from principal
type connectionEntries struct {
	// lock should be acquired before reading/writing to connMap
	lock sync.RWMutex

	// connMap maintains a list of when a ping was last seen from each connection (uuid). This allows us to close stale connections.
	// - key is connection uuid
	connMap map[string]connectionEntry // acquire lock before accessing
}

// connectionEntry describes statistics about a specific connection (uuid)
type connectionEntry struct {
	// lastPing the time at which the last ping message was received from principal
	lastPing time.Time
}

const (
	// principalRedisConnectionTimeout is the length of time the agent will wait to receive redis communication from principal, before it will close the connection. e.g. if we receive no requests for 10 minutes, we receive no heartbeats (pings) the connection is no longer required.
	principalRedisConnectionTimeout = 10 * time.Minute
)

// processIncomingRedisRequest handles incoming redis-specific events from principal, including get/subscribe (from argo cd) and ping (internal).
func (a *Agent) processIncomingRedisRequest(ev *event.Event) error {

	rreq, err := ev.RedisRequest()
	if err != nil {
		return err
	}
	logCtx := log().WithFields(logrus.Fields{
		"method":         "processIncomingRedisRequest",
		"uuid":           rreq.UUID,
		"connectionUUID": rreq.ConnectionUUID,
	})

	logCtx.Tracef("Start processing incoming redis request %v", rreq)

	var responseBody *event.RedisResponseBody

	if rreq.Body.Get != nil {
		var err error

		responseBody, err = a.handleRedisGetMessage(logCtx, rreq)
		if err != nil {
			return err
		}

	} else if rreq.Body.Subscribe != nil {

		var err error
		responseBody, err = a.handleRedisSubscribeMessage(logCtx, rreq)
		if err != nil {
			return err
		}

	} else if rreq.Body.Ping != nil {

		// We have seen a ping for this connection, so update the lifecycle struct
		connections := a.redisProxyMsgHandler.connections

		connections.lock.Lock()
		connections.connMap[rreq.ConnectionUUID] = connectionEntry{
			lastPing: time.Now(),
		}
		connections.lock.Unlock()

		// Send pong back to principal
		responseBody = &event.RedisResponseBody{
			Pong: &event.RedisResponseBodyPong{},
		}
		logCtx.Tracef("Queuing Redis pong response")

	} else {
		logCtx.Error("Unrecognized redis request body")
		return fmt.Errorf("unrecognized redis request body")
	}

	if responseBody == nil {
		logCtx.Error("unexpected lack of response to redis request: response command not defined")
		return fmt.Errorf("unexpected lack of response to redis request")
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Remote queue disappeared")
		return nil
	}
	q.Add(a.emitter.NewRedisResponseEvent(rreq.UUID, rreq.ConnectionUUID, *responseBody))
	logCtx.Trace("Emitted redis resource response")

	return nil
}

func (a *Agent) handleRedisSubscribeMessage(logCtx *logrus.Entry, rreq *event.RedisRequest) (*event.RedisResponseBody, error) {
	channelName := rreq.Body.Subscribe.ChannelName

	logCtx.Tracef("Start processing redis SUBSCRIBE request with key '%s'", channelName)

	channelName, err := stripNamespaceFromRedisKey(channelName, logCtx)
	if err != nil {
		return nil, fmt.Errorf("unable to transform SUBSCRIBE key for autonomous agent: %v", err)
	}

	// Subscribe to the argo cd redis channel via redis client
	pubsub := a.redisProxyMsgHandler.argoCDRedisClient.Subscribe(context.Background(), channelName)

	// Send back a response to principal, to indicate the subscription command was received
	res := &event.RedisResponseBody{
		SubscribeResponse: &event.RedisResponseBodySubscribeResponse{
			Error: "", // no error
		},
	}

	// Set the lastPing to the current time, as we know the connection is valid as of now
	connLifecycle := a.redisProxyMsgHandler.connections

	connLifecycle.lock.Lock()
	defer connLifecycle.lock.Unlock()
	connLifecycle.connMap[rreq.ConnectionUUID] = connectionEntry{
		lastPing: time.Now(),
	}

	// Start a new go-routine to read the redis subscribe channel for events, and send them back asynchronously
	go func() {
		a.forwardRedisSubscribeNotificationsToPrincipal(pubsub, rreq, channelName, logCtx)
	}()

	return res, nil

}

// forwardRedisSubscribeNotificationsToPrincipal reads subscription events from agent Argo CD redis, then sends them to principal.
// - This function will also close local redis connections for principal connections that are no longer active (based on pings received)
func (a *Agent) forwardRedisSubscribeNotificationsToPrincipal(pubsub *redis.PubSub, rreq *event.RedisRequest, channelName string, logCtx *logrus.Entry) {
	ticker := time.NewTicker(1 * time.Minute)

	ch := pubsub.Channel()

	logCtx = logCtx.WithField("channel-name", channelName)
	for {
		select {

		case <-ticker.C:
			// Every X minutes, check if the connection is still active based on when we received the last ping

			connLifecycle := a.redisProxyMsgHandler.connections

			var lastPing *time.Time
			connLifecycle.lock.RLock()
			entry, ok := connLifecycle.connMap[rreq.ConnectionUUID]
			if ok {
				lastPing = &entry.lastPing
			}
			connLifecycle.lock.RUnlock()

			if lastPing == nil {
				logCtx.Error("lastPing for a connection should never be nil")
				continue
			}

			// If the connection has not been active for X minutes, close the connection
			if time.Since(*lastPing) >= principalRedisConnectionTimeout {
				pubsub.Close()
				logCtx.WithField("lastPing", lastPing).Trace("closing redis connection due to inactivity")
				return
			}

		case readFromChannel := <-ch:

			logCtx.Tracef("Redis subscription '%s' returned value of %d bytes", channelName, len(readFromChannel.Payload))
			// We don't output the content of the returned value, as it may contain sensitive information (and also because it's often binary)

			// The only subscription messages we expect/support have an empty payload
			if len(readFromChannel.Payload) != 0 || len(readFromChannel.PayloadSlice) != 0 {
				logCtx.Errorf("unexpected payload length from Redis subscription: %d %d", len(readFromChannel.Payload), len(readFromChannel.PayloadSlice))
				continue
			}

			pushEventToPrincipal := event.RedisResponseBody{}

			pushEventToPrincipal.PushFromSubscribe = &event.RedisResponseBodyPushFromSubscribe{
				ChannelName: rreq.Body.Subscribe.ChannelName,
			}

			q := a.queues.SendQ(defaultQueueName)
			if q == nil {
				logCtx.Error("Remote queue disappeared")
				return
			}
			q.Add(a.emitter.NewRedisResponseEvent(rreq.UUID, rreq.ConnectionUUID, pushEventToPrincipal))
			logCtx.Tracef("Emitted Subscribe push event")
		}
	}

}

func (a *Agent) handleRedisGetMessage(logCtx *logrus.Entry, rreq *event.RedisRequest) (*event.RedisResponseBody, error) {
	key := rreq.Body.Get.Key

	logCtx.Tracef("Start processing redis GET request with key '%s'", key)

	key, err := stripNamespaceFromRedisKey(key, logCtx)
	if err != nil {
		return nil, fmt.Errorf("unable to transform GET key for autonomous agent: %v", err)
	}

	// Connect to local redis to GET key/value, and store response
	getBody := event.RedisResponseBodyGet{}

	var data []byte
	err = a.redisProxyMsgHandler.argoCDRedisCache.GetSkippingLocalCache(context.Background(), key, &data)
	if errors.Is(err, rediscache.ErrCacheMiss) {
		getBody.Bytes = nil
		getBody.CacheHit = false
		getBody.Error = ""
	} else if err != nil {
		getBody.Bytes = nil
		getBody.CacheHit = false
		getBody.Error = err.Error()
	} else {
		getBody.Bytes = data
		getBody.CacheHit = true
		getBody.Error = ""
	}

	res := &event.RedisResponseBody{
		Get: &getBody,
	}

	logCtx.Tracef("Queuing Redis GET '%s' response: %d bytes / %v / %v", key, len(getBody.Bytes), getBody.CacheHit, getBody.Error)

	return res, nil
}

// stripNamespaceFromRedisKey will remove the namespace from the namespace_name field of the redis key.
//
// Example:
// "app|managed-resources|agent-autonomous_my-app|1.8.3"
// "app|resources-tree|agent-autonomous_my-app|1.8.3.gz
// needs to be converted to, e.g.
// "app|resources-tree|my-app|1.8.3.gz
func stripNamespaceFromRedisKey(key string, logCtx *logrus.Entry) (string, error) {

	var matchedPrefix string
	expectedPrefixes := []string{
		"app|resources-tree|",
		"app|managed-resources|",
	}
	for _, expectedPrefix := range expectedPrefixes {
		if strings.HasPrefix(key, expectedPrefix) {
			matchedPrefix = expectedPrefix
			break
		}
	}
	if matchedPrefix == "" {
		err := fmt.Errorf("unexpected redis key request: '%s'", key)
		logCtx.WithError(err).Error("Unable to reply to redis get request")
		return "", err
	}

	components := strings.Split(key, "|")

	if len(components) != 4 {
		err := fmt.Errorf("unexpected key format: '%s'", key)
		logCtx.WithError(err).Error("Unable to reply to redis get request")
		return "", err
	}

	appName := components[2]

	underscoreIndex := strings.Index(appName, "_")
	if underscoreIndex == -1 {
		err := fmt.Errorf("unexpected key format, missing '_': '%s'", key)
		logCtx.WithError(err).Error("Unable to reply to redis get request")
		return "", err

	}
	components[2] = appName[underscoreIndex+1:]

	key = strings.Join(components, "|")

	return key, nil

}

func (a *Agent) getRedisClientAndCache() (*redis.Client, *rediscache.Cache, error) {
	var tlsConfig *tls.Config = nil

	opts := &redis.Options{
		Addr:       a.redisProxyMsgHandler.redisAddress,
		Password:   a.redisProxyMsgHandler.redisPassword,
		DB:         0,
		MaxRetries: 3,
		TLSConfig:  tlsConfig,
		Username:   a.redisProxyMsgHandler.redisUsername,
	}

	client := redis.NewClient(opts)
	cache := rediscache.New(&rediscache.Options{Redis: client})

	return client, cache, nil

}
