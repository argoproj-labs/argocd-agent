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

package queue

import (
	"context"
	"fmt"
	"sync"

	"github.com/cloudevents/sdk-go/v2/event"
	"k8s.io/client-go/util/workqueue"
)

var _ QueuePair = &SendRecvQueues{}

// QueuePair maintains a map (indexed by name) of send/receive queue pairs
type QueuePair interface {
	Names() []string
	HasQueuePair(name string) bool
	Len() int
	SendQ(name string) workqueue.TypedRateLimitingInterface[*event.Event]
	RecvQ(name string) workqueue.TypedRateLimitingInterface[*event.Event]
	Create(name string) error
	Delete(name string, shutdown bool) error
}

const (
	// defaultMaxQueueSize is the max number of items that the workqueue can hold.
	defaultMaxQueueSize int = 1000
)

type queuepair struct {
	recvq *boundedQueue
	sendq *boundedQueue
}

type boundedQueue struct {
	workqueue.TypedRateLimitingInterface[*event.Event]
	maxSize int

	notify chan struct{}
}

func newBoundedQueue(maxSize int) *boundedQueue {
	rateLimiter := workqueue.DefaultTypedControllerRateLimiter[*event.Event]()
	return &boundedQueue{
		TypedRateLimitingInterface: workqueue.NewTypedRateLimitingQueue(rateLimiter),
		maxSize:                    maxSize,
		notify:                     make(chan struct{}, 10),
	}
}

func (bq *boundedQueue) Add(item *event.Event) {
	// We pop the oldest item if the size is going to exceed maxSize.
	if bq.Len() == bq.maxSize {
		old, _ := bq.Get()
		bq.Done(old)
	}

	bq.TypedRateLimitingInterface.Add(item)

	// Notify any waiting goroutines that an item has been added to the queue.
	select {
	case bq.notify <- struct{}{}:
	default:
		// We don't want to block the caller if the notify channel is full.
		return
	}
}

type SendRecvQueues struct {
	queues    map[string]*queuepair
	queuelock sync.RWMutex
}

func NewSendRecvQueues() *SendRecvQueues {
	return &SendRecvQueues{
		queues: make(map[string]*queuepair),
	}
}

// Names returns the names of all currently existing queues.
func (q *SendRecvQueues) Names() []string {
	// TODO(jannfis): This is a potentially expensive operation at O(n), and
	// it keeps a read lock on the queues at all times. We might want to find
	// a better way, potentially at the expense of memory.
	q.queuelock.RLock()
	defer q.queuelock.RUnlock()
	names := make([]string, 0, len(q.queues))
	for k := range q.queues {
		names = append(names, k)
	}
	return names
}

// HasQueuePair returns true if a queue pair with name currently exists
func (q *SendRecvQueues) HasQueuePair(name string) bool {
	q.queuelock.RLock()
	defer q.queuelock.RUnlock()
	_, ok := q.queues[name]
	return ok
}

// Len returns the number of queue pairs held by q
func (q *SendRecvQueues) Len() int {
	q.queuelock.RLock()
	defer q.queuelock.RUnlock()
	return len(q.queues)
}

// SendQ will return the send queue from the queue pair named name. If no such
// queue pair exists, returns nil
func (q *SendRecvQueues) SendQ(name string) workqueue.TypedRateLimitingInterface[*event.Event] {
	q.queuelock.RLock()
	defer q.queuelock.RUnlock()
	qp, ok := q.queues[name]
	if ok {
		return qp.sendq
	}
	return nil
}

// RecvQ will return the receive queue from the queue pair named name. If no
// such queue pair exists, returns nil
func (q *SendRecvQueues) RecvQ(name string) workqueue.TypedRateLimitingInterface[*event.Event] {
	q.queuelock.RLock()
	defer q.queuelock.RUnlock()
	qp, ok := q.queues[name]
	if ok {
		return qp.recvq
	}
	return nil
}

// Create creates and initializes a queue pair with name, and adds it to the
// list of available queues. The given name must be unique, if a queue pair
// with the same name already exists, Create will return an error.
func (q *SendRecvQueues) Create(name string) error {
	q.queuelock.Lock()
	defer q.queuelock.Unlock()
	_, ok := q.queues[name]
	if ok {
		return fmt.Errorf("cannot initialize queue for %s: queue already exists", name)
	}
	qp := &queuepair{}
	qp.sendq = newBoundedQueue(defaultMaxQueueSize)
	qp.recvq = newBoundedQueue(defaultMaxQueueSize)
	q.queues[name] = qp

	return nil
}

// Delete will delete the named queue pair from the list of available queue.
// pairs. If shutdown is true, the Shutdown function will be called on both
// send and receive queues. If the named queue does not exist, Delete will
// return an error.
func (q *SendRecvQueues) Delete(name string, shutdown bool) error {
	q.queuelock.Lock()
	defer q.queuelock.Unlock()
	queue, ok := q.queues[name]
	if !ok {
		return fmt.Errorf("cannot drop queue %s: queue does not exist", name)
	}
	if shutdown {
		queue.recvq.ShutDown()
		queue.sendq.ShutDown()
	}
	delete(q.queues, name)
	return nil
}

// GetWithContext is a wrapper around the workqueue's Get method.
// It waits until an item is available in the queue or the context is Done
func GetWithContext(q workqueue.TypedRateLimitingInterface[*event.Event], ctx context.Context) (*event.Event, bool) {
	bq, ok := q.(*boundedQueue)
	if !ok {
		return nil, false
	}

	for {
		if bq.Len() > 0 {
			return bq.Get()
		}

		// Suspend until an item is available or context is cancelled
		select {
		case <-ctx.Done():
			return nil, false
		case <-bq.notify:
			// Wake up and re-check if an item is available
		}
	}
}
