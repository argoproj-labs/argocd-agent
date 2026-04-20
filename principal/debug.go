// Copyright 2026 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package principal

import (
	"runtime"
	"time"
)

// logDebugOutput prints some debugging information about the server to the logs
func (s *Server) logDebugOutput() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	logCtx := log().WithField("module", "debugRoutine")
	logCtx.Infof("HeapAlloc=%d HeapSys=%d HeapIdle=%d HeapInuse=%d HeapReleased=%d HeapObjects=%d", m.Alloc/1024/1024, m.Sys/1024/1024, m.HeapIdle/1024/1024, m.HeapInuse/1024/1024, m.HeapReleased/1024/1024, m.HeapObjects)
	logCtx.Infof("GoRoutines=%d", runtime.NumGoroutine())
	logCtx.Infof("QueuePairs=%d", s.queues.Len())
	totalQueueDepth := 0
	for _, queueName := range s.queues.Names() {
		sendLen := s.queues.SendQ(queueName).Len()
		recvLen := s.queues.RecvQ(queueName).Len()
		if recvLen > 0 {
			logCtx.Infof("RecvQueueDepth(%s)=%d", queueName, s.queues.RecvQ(queueName).Len())
		}
		if sendLen > 0 {
			logCtx.Infof("SendQueueDepth(%s)=%d", queueName, s.queues.SendQ(queueName).Len())
		}
		totalQueueDepth += sendLen + recvLen
	}
	logCtx.Infof("TotalQueueDepth=%d", totalQueueDepth)
}

// scheduleDebugRoutine launches a goroutine that prints debug information about the server to the logs every 10 seconds
func (s *Server) scheduleDebugRoutine() {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				s.logDebugOutput()
				ticker.Reset(30 * time.Second)
			case <-s.ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}
