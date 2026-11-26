// Copyright 2026 The argocd-agent Authors
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

package manager

import (
	"maps"
	"strings"
	"sync/atomic"

	syncommon "github.com/argoproj/argo-cd/gitops-engine/v3/pkg/sync/common"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/util/glob"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/equality"
)

// annotationClass is the synchronization behaviour that applies to an
// annotation key on an Argo CD Application. Every key belongs to exactly one
// class, which decides whether the key is transferred between the principal
// and an agent and which side owns its value.
type annotationClass int

const (
	// annotationIgnore means the annotation is neither transferred nor reverted.
	annotationIgnore annotationClass = iota
	// annotationMirror means the peer's value is owned by the source of truth.
	annotationMirror
	// annotationPreserve means the key is carried over rather than mirrored, and is
	// never deleted by the merge.
	annotationPreserve
)

const (
	// agentAnnotationPrefix is argocd-agent's own annotation namespace. Everything under
	// it is bookkeeping written by the agent or the principal, so it is
	// preserved rather than mirrored.
	agentAnnotationPrefix = "argocd-agent.argoproj.io/"

	// noticeAnnotationPrefix is the prefix Argo CD uses for user-visible notices on an
	// Application. These are most useful travelling from an autonomous agent up
	// to the principal, but mirroring them in both directions does no harm.
	noticeAnnotationPrefix = "notice.argocd.argoproj.io/"

	// lastAppliedConfiguration is written by 'kubectl apply'. It embeds a copy
	// of the object including its namespace, so transferring it to the peer
	// leaves a manifest snapshot that refers to the wrong namespace.
	lastAppliedConfiguration = "kubectl.kubernetes.io/last-applied-configuration"

	// ignoreResourceUpdates has no exported constant in the Argo CD modules.
	ignoreResourceUpdates = "argocd.argoproj.io/ignore-resource-updates"
)

// mirrorKeys follow the source-of-truth to peer behaviour.
var mirrorKeys = map[string]struct{}{
	v1alpha1.AnnotationKeyManifestGeneratePaths: {},
	common.AnnotationKeyAppSkipReconcile:        {},
}

// TriggerAnnotations are one-shot requests rather than state: something sets
// one, the application controller acts on it and then removes it. The request
// has to reach the peer whichever side made it, and the removal has to travel
// back, or the annotation is stranded on the side that did not consume it.
// Argo CD writes both keys for a single refresh, so they travel together.
var TriggerAnnotations = []string{
	v1alpha1.AnnotationKeyRefresh,
	v1alpha1.AnnotationKeyRefreshTimestamp,
}

// preserveKeys are argocd-agent's functional annotations and the trigger
// annotations, whose lifecycles are managed explicitly elsewhere.
var preserveKeys = func() map[string]struct{} {
	keys := map[string]struct{}{
		SourceUIDAnnotation:         {},
		PrincipalUIDAnnotation:      {},
		NotifiedAnnotation:          {},
		NamespaceRemappedAnnotation: {},
		MismatchPolicyAnnotation:    {},
		AdoptionPolicyAnnotation:    {},
	}
	for _, k := range TriggerAnnotations {
		keys[k] = struct{}{}
	}
	return keys
}()

// childAppKeys are only meaningful on a child Application inside an
// app-of-apps. argocd-agent never transfers a parent app-of-apps between
// control planes, so these can never apply to an Application it syncs. They are
// listed explicitly so that an operator cannot allow-list them by accident.
var childAppKeys = map[string]struct{}{
	common.AnnotationCompareOptions:         {},
	syncommon.AnnotationKeyHook:             {},
	syncommon.AnnotationKeyHookDeletePolicy: {},
	syncommon.AnnotationSyncOptions:         {},
	syncommon.AnnotationSyncWave:            {},
	ignoreResourceUpdates:                   {},
}

// AnnotationFilter classifies annotation keys and applies the behaviour of
// each class. The allow list is swapped atomically so it can be replaced while
// events are in flight. The zero value is unusable; call NewAnnotationFilter.
type AnnotationFilter struct {
	allow atomic.Pointer[[]string]
}

// NewAnnotationFilter returns a AnnotationFilter whose allow list holds the accepted entries of
// patterns. Rejected entries are logged and dropped.
func NewAnnotationFilter(patterns []string) *AnnotationFilter {
	f := &AnnotationFilter{}
	f.SetAllowList(patterns)
	return f
}

// SetAllowList replaces the user-defined allow list. Patterns capturing a key
// argocd-agent depends on are rejected, logged and skipped rather than treated
// as fatal: the list is editable at runtime, so a typo must not take the
// component down.
func (f *AnnotationFilter) SetAllowList(patterns []string) {
	accepted := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if reserved := f.reservedMatch(p); reserved != "" {
			annotationLog().Warnf("Ignoring annotation allow-list entry %q: it matches the reserved annotation %q", p, reserved)
			continue
		}
		accepted = append(accepted, p)
	}
	f.allow.Store(&accepted)
}

// AllowList returns the accepted allow-list patterns currently in effect.
func (f *AnnotationFilter) AllowList() []string {
	if p := f.allow.Load(); p != nil {
		return append([]string(nil), *p...)
	}
	return nil
}

// reservedMatch reports the first reserved key that pattern would capture, or
// the empty string when the pattern is safe to accept.
func (f *AnnotationFilter) reservedMatch(pattern string) string {
	for _, key := range reservedKeys() {
		if glob.MatchStringInList([]string{pattern}, key, glob.GLOB) {
			return key
		}
	}
	return ""
}

// reservedKeys are the keys an allow-list entry may never capture.
func reservedKeys() []string {
	keys := make([]string, 0, len(preserveKeys)+len(childAppKeys)+2)
	for k := range preserveKeys {
		keys = append(keys, k)
	}
	for k := range childAppKeys {
		keys = append(keys, k)
	}
	// The tracking annotation identifies the Argo CD instance that owns a live
	// resource; copying it across would make the peer claim ownership.
	keys = append(keys, common.AnnotationKeyAppInstance, lastAppliedConfiguration)
	return keys
}

// classify returns the behaviour that applies to key.
func (f *AnnotationFilter) classify(key string) annotationClass {
	if _, ok := preserveKeys[key]; ok {
		return annotationPreserve
	}
	if strings.HasPrefix(key, agentAnnotationPrefix) {
		return annotationPreserve
	}
	if _, ok := childAppKeys[key]; ok {
		return annotationIgnore
	}
	if _, ok := mirrorKeys[key]; ok {
		return annotationMirror
	}
	if strings.HasPrefix(key, noticeAnnotationPrefix) {
		return annotationMirror
	}
	if allow := f.allow.Load(); allow != nil && len(*allow) > 0 {
		if glob.MatchStringInList(*allow, key, glob.GLOB) {
			return annotationMirror
		}
	}
	return annotationIgnore
}

// Outbound returns the annotations that may be placed on an event. Ignored keys
// are dropped so that they neither reach the peer nor appear in the payload.
// A nil input yields a nil result, so an object without annotations does not
// gain an empty map on the wire.
func (f *AnnotationFilter) Outbound(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if f.classify(k) != annotationIgnore {
			out[k] = v
		}
	}
	return out
}

// MirrorOnly returns just the annotations the source of truth owns. It is
// what gets cached for drift detection: anything else on the peer is not the
// source of truth's to restore.
func (f *AnnotationFilter) MirrorOnly(in map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range in {
		if f.classify(k) == annotationMirror {
			out[k] = v
		}
	}
	return out
}

// RestoreMirrored rewrites the mirrored annotations on app to match want,
// adding and updating the ones it lists and removing any other mirrored key.
// Annotations of every other class are left exactly as they are.
func (f *AnnotationFilter) RestoreMirrored(app map[string]string, want map[string]string) map[string]string {
	out := make(map[string]string, len(app)+len(want))
	for k, v := range app {
		if f.classify(k) == annotationMirror {
			continue
		}
		out[k] = v
	}
	for k, v := range want {
		out[k] = v
	}
	return out
}

// Merge computes the annotation map to write onto the object already on the
// cluster. Mirrored keys come wholly from incoming, so a deletion on the
// source of truth propagates; preserved keys come from incoming when present
// and are otherwise retained; all other keys are kept from existing and
// discarded from incoming.
func (f *AnnotationFilter) Merge(existing, incoming map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(incoming))

	for k, v := range existing {
		// Mirrored keys are re-established from incoming below; dropping them
		// here is what lets a deletion on the source of truth propagate.
		if f.classify(k) == annotationMirror {
			continue
		}
		out[k] = v
	}

	for k, v := range incoming {
		switch f.classify(k) {
		case annotationMirror, annotationPreserve:
			out[k] = v
		}
	}

	return out
}

func annotationLog() *logrus.Entry {
	return logrus.WithField("component", "AnnotationFilter")
}

// AppChangeFilter reports whether an informer update is worth acting on. An
// update whose only difference is in annotations that are never transferred
// is rejected, so that editing one does not produce an event the peer would
// discard on arrival.
func (f *AnnotationFilter) AppChangeFilter(old, new *v1alpha1.Application) bool {
	if old == nil || new == nil {
		return true
	}

	// Fast path: this filter only ever suppresses annotation changes, so an
	// untouched annotation map spares the deep copy below.
	if maps.Equal(old.Annotations, new.Annotations) {
		return true
	}

	if !maps.Equal(f.Outbound(old.Annotations), f.Outbound(new.Annotations)) {
		return true
	}

	// Only untransferred annotations changed: act solely if something outside
	// the annotations changed too.
	return !f.annotationsOnlyChange(old, new)
}

// annotationsOnlyChange reports whether old and new differ exclusively in
// their annotations.
func (f *AnnotationFilter) annotationsOnlyChange(old, new *v1alpha1.Application) bool {
	a := old.DeepCopy()
	b := new.DeepCopy()

	// Ignore the annotations themselves, plus the fields that change on every
	// write regardless of content.
	for _, app := range []*v1alpha1.Application{a, b} {
		app.Annotations = nil
		app.ResourceVersion = ""
		app.ManagedFields = nil
	}

	return equality.Semantic.DeepEqual(a, b)
}
