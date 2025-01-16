// Copyright 2024 The argocd-agent Authors
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

package resourceproxy

import (
	"fmt"
	"sync"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
)

// requestWrapper holds information about a given request to be tracked.
type requestWrapper struct {
	// agentName is the name of the agent for which a resource is tracked
	agentName string
	// evCh is a channel where the requester expects the response to its
	// requests is written to.
	evCh chan *cloudevents.Event
}

// requestState holds all currently tracked requests
type requestState struct {
	mutex    sync.RWMutex
	requests map[string]*requestWrapper
}

// Tracked returns the tracked event identified by resId
func (p *ResourceProxy) Tracked(eventId string) (string, chan *cloudevents.Event) {
	p.statemap.mutex.RLock()
	defer p.statemap.mutex.RUnlock()
	r, ok := p.statemap.requests[eventId]
	if !ok || r == nil {
		return "", nil
	}
	return r.agentName, r.evCh
}

// Track starts tracking a resource request identified by its UUID for an agent
// with the given name. It will return a channel the caller can use to read the
// response event from.
func (p *ResourceProxy) Track(eventId string, agentName string) (<-chan *cloudevents.Event, error) {
	p.statemap.mutex.Lock()
	defer p.statemap.mutex.Unlock()
	log().WithFields(logrus.Fields{"event_id": eventId, "agent": agentName}).Trace("Tracking new request")
	_, ok := p.statemap.requests[eventId]
	if ok {
		return nil, fmt.Errorf("resource with ID %s already tracked", eventId)
	}
	ch := make(chan *cloudevents.Event, 1)
	p.statemap.requests[eventId] = &requestWrapper{agentName: agentName, evCh: ch}
	return ch, nil
}

// StopTracking will stop tracking a particular resource and close any event
// channel that may still be open.
func (p *ResourceProxy) StopTracking(eventId string) error {
	p.statemap.mutex.Lock()
	defer p.statemap.mutex.Unlock()
	r, ok := p.statemap.requests[eventId]
	if ok {
		close(r.evCh)
		logCtx := log().WithFields(logrus.Fields{
			"event_id": eventId,
			"agent":    r.agentName,
		})
		logCtx.Trace("Finished tracking request")
		delete(p.statemap.requests, eventId)
		return nil
	} else {
		log().WithField("event_id", eventId).Warn("Resource not tracked -- is this a bug?")
		return fmt.Errorf("resource request %s not tracked", eventId)
	}
}
