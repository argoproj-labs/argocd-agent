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
	"testing"

	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClassify(t *testing.T) {
	f := NewAnnotationFilter(nil)

	for _, tc := range []struct {
		name string
		key  string
		want annotationClass
	}{
		// Mirrored: the source of truth owns these on the peer.
		{"manifest-generate-paths", v1alpha1.AnnotationKeyManifestGeneratePaths, annotationMirror},
		{"skip-reconcile", common.AnnotationKeyAppSkipReconcile, annotationMirror},
		{"notice prefix", noticeAnnotationPrefix + "something", annotationMirror},
		{"notice prefix with path", noticeAnnotationPrefix + "a/b", annotationMirror},

		// Preserved: argocd-agent's own bookkeeping and the refresh annotation.
		{"source-uid", SourceUIDAnnotation, annotationPreserve},
		{"principal-uid", PrincipalUIDAnnotation, annotationPreserve},
		{"notified", NotifiedAnnotation, annotationPreserve},
		{"namespace-remapped", NamespaceRemappedAnnotation, annotationPreserve},
		{"mismatch-policy", MismatchPolicyAnnotation, annotationPreserve},
		{"adoption-policy", AdoptionPolicyAnnotation, annotationPreserve},
		{"refresh", v1alpha1.AnnotationKeyRefresh, annotationPreserve},
		{"agent prefix", agentAnnotationPrefix + "last-updated", annotationPreserve},

		// Ignored: only meaningful on a child app of an app-of-apps.
		{"compare-options", common.AnnotationCompareOptions, annotationIgnore},
		{"hook", "argocd.argoproj.io/hook", annotationIgnore},
		{"hook-delete-policy", "argocd.argoproj.io/hook-delete-policy", annotationIgnore},
		{"sync-options", "argocd.argoproj.io/sync-options", annotationIgnore},
		{"sync-wave", "argocd.argoproj.io/sync-wave", annotationIgnore},
		{"ignore-resource-updates", ignoreResourceUpdates, annotationIgnore},

		// Ignored: unknown to argocd-agent.
		{"arbitrary user key", "example.com/owner", annotationIgnore},
		{"image updater without allow list", "argocd-image-updater.argoproj.io/image-list", annotationIgnore},
		{"last-applied-configuration", lastAppliedConfiguration, annotationIgnore},
		{"tracking id", common.AnnotationKeyAppInstance, annotationIgnore},
		{"empty key", "", annotationIgnore},
		{"notice prefix without trailing path", "notice.argocd.argoproj.io", annotationIgnore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, f.classify(tc.key), "key %q", tc.key)
		})
	}
}

func TestClassifyWithAllowList(t *testing.T) {
	f := NewAnnotationFilter([]string{"argocd-image-updater.argoproj.io/*", "example.com/exact"})

	assert.Equal(t, annotationMirror, f.classify("argocd-image-updater.argoproj.io/image-list"))
	assert.Equal(t, annotationMirror, f.classify("argocd-image-updater.argoproj.io/git-branch"))
	assert.Equal(t, annotationMirror, f.classify("example.com/exact"))
	assert.Equal(t, annotationIgnore, f.classify("example.com/other"))

	// The allow list must not be able to reclassify what argocd-agent owns.
	assert.Equal(t, annotationPreserve, f.classify(SourceUIDAnnotation))
	assert.Equal(t, annotationIgnore, f.classify("argocd.argoproj.io/sync-wave"))
}

func TestSetAllowListRejectsReservedKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
	}{
		{"source-uid exactly", SourceUIDAnnotation},
		{"sync-wave exactly", "argocd.argoproj.io/sync-wave"},
		{"tracking id exactly", common.AnnotationKeyAppInstance},
		{"last-applied-configuration", lastAppliedConfiguration},
		{"wildcard capturing argocd keys", "argocd.argoproj.io/*"},
		{"wildcard capturing everything", "*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := NewAnnotationFilter([]string{tc.pattern})
			assert.Empty(t, f.AllowList(), "pattern %q should have been rejected", tc.pattern)
		})
	}
}

func TestSetAllowListKeepsSafeEntriesAlongsideRejected(t *testing.T) {
	// A bad entry must not discard the good ones: the allow list is editable at
	// runtime and a typo should degrade, not disable.
	f := NewAnnotationFilter([]string{"argocd.argoproj.io/*", "example.com/*", "  ", ""})
	assert.Equal(t, []string{"example.com/*"}, f.AllowList())
	assert.Equal(t, annotationMirror, f.classify("example.com/anything"))
}

func TestSetAllowListReplacesPrevious(t *testing.T) {
	f := NewAnnotationFilter([]string{"example.com/*"})
	require.Equal(t, annotationMirror, f.classify("example.com/a"))

	f.SetAllowList([]string{"other.com/*"})
	assert.Equal(t, annotationIgnore, f.classify("example.com/a"))
	assert.Equal(t, annotationMirror, f.classify("other.com/a"))

	// Clearing the list reverts every user key to being ignored.
	f.SetAllowList(nil)
	assert.Equal(t, annotationIgnore, f.classify("other.com/a"))
	assert.Empty(t, f.AllowList())
}

func TestOutbound(t *testing.T) {
	f := NewAnnotationFilter([]string{"example.com/keep"})

	t.Run("nil stays nil", func(t *testing.T) {
		assert.Nil(t, f.Outbound(nil))
	})

	t.Run("drops only ignored keys", func(t *testing.T) {
		got := f.Outbound(map[string]string{
			common.AnnotationKeyAppSkipReconcile: "true",
			SourceUIDAnnotation:                  "uid",
			v1alpha1.AnnotationKeyRefresh:        "normal",
			"example.com/keep":                   "yes",
			"argocd.argoproj.io/sync-wave":       "5",
			"example.com/drop":                   "no",
			lastAppliedConfiguration:             "{}",
		})
		assert.Equal(t, map[string]string{
			common.AnnotationKeyAppSkipReconcile: "true",
			SourceUIDAnnotation:                  "uid",
			v1alpha1.AnnotationKeyRefresh:        "normal",
			"example.com/keep":                   "yes",
		}, got)
	})

	t.Run("does not mutate its input", func(t *testing.T) {
		in := map[string]string{"argocd.argoproj.io/sync-wave": "5"}
		_ = f.Outbound(in)
		assert.Len(t, in, 1)
	})
}

func TestMerge(t *testing.T) {
	f := NewAnnotationFilter(nil)
	mirror := common.AnnotationKeyAppSkipReconcile

	t.Run("mirrored key is added from incoming", func(t *testing.T) {
		got := f.Merge(nil, map[string]string{mirror: "true"})
		assert.Equal(t, "true", got[mirror])
	})

	t.Run("mirrored key is updated from incoming", func(t *testing.T) {
		got := f.Merge(map[string]string{mirror: "false"}, map[string]string{mirror: "true"})
		assert.Equal(t, "true", got[mirror])
	})

	t.Run("mirrored key absent from incoming is deleted", func(t *testing.T) {
		got := f.Merge(map[string]string{mirror: "true"}, map[string]string{})
		assert.NotContains(t, got, mirror)
	})

	t.Run("ignored key on existing survives", func(t *testing.T) {
		got := f.Merge(map[string]string{"example.com/local": "yes"}, map[string]string{mirror: "true"})
		assert.Equal(t, "yes", got["example.com/local"], "an unknown annotation on the peer must never be removed")
	})

	t.Run("ignored key on incoming is discarded", func(t *testing.T) {
		got := f.Merge(map[string]string{}, map[string]string{"argocd.argoproj.io/sync-wave": "5"})
		assert.NotContains(t, got, "argocd.argoproj.io/sync-wave")
	})

	t.Run("preserved key is kept when incoming omits it", func(t *testing.T) {
		got := f.Merge(map[string]string{SourceUIDAnnotation: "uid"}, map[string]string{})
		assert.Equal(t, "uid", got[SourceUIDAnnotation])
	})

	t.Run("preserved key is taken from incoming when present", func(t *testing.T) {
		got := f.Merge(
			map[string]string{SourceUIDAnnotation: "old"},
			map[string]string{SourceUIDAnnotation: "new"},
		)
		assert.Equal(t, "new", got[SourceUIDAnnotation])
	})

	t.Run("regression: unrelated update does not clobber a peer-local key", func(t *testing.T) {
		// This is the behaviour probe B3 demonstrated was broken: a spec change
		// on the source of truth used to delete annotations set on the peer.
		existing := map[string]string{
			"example.com/local": "yes",
			SourceUIDAnnotation: "uid",
		}
		incoming := map[string]string{
			SourceUIDAnnotation: "uid",
			mirror:              "true",
		}
		got := f.Merge(existing, incoming)
		assert.Equal(t, map[string]string{
			"example.com/local": "yes",
			SourceUIDAnnotation: "uid",
			mirror:              "true",
		}, got)
	})

	t.Run("both nil yields an empty map", func(t *testing.T) {
		assert.Empty(t, f.Merge(nil, nil))
	})
}

// TestTriggerAnnotationsArePreserved pins the classification of the one-shot
// trigger annotations. Only the bare refresh key used to be handled, so the
// timestamp Argo CD writes alongside it was dropped and the peer received a
// partial refresh request.
func TestTriggerAnnotationsArePreserved(t *testing.T) {
	f := NewAnnotationFilter(nil)

	require.ElementsMatch(t, []string{
		"argocd.argoproj.io/refresh",
		"argocd.argoproj.io/refresh-timestamp",
	}, TriggerAnnotations)

	for _, key := range TriggerAnnotations {
		t.Run(key, func(t *testing.T) {
			assert.Equal(t, annotationPreserve, f.classify(key),
				"a trigger annotation must never be dropped by the generic merge")

			// It has to survive the outbound filter, or the request would
			// never reach the peer in the first place.
			assert.Contains(t, f.Outbound(map[string]string{key: "normal"}), key)

			// It must not be reclassifiable by an operator.
			bad := NewAnnotationFilter([]string{key})
			assert.Empty(t, bad.AllowList(), "a trigger annotation must be rejected from the allow list")
		})
	}
}

func testApp(resourceVersion string, anns map[string]string) *v1alpha1.Application {
	return &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:            "guestbook",
			Namespace:       "argocd",
			ResourceVersion: resourceVersion,
			Annotations:     anns,
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source:  &v1alpha1.ApplicationSource{RepoURL: "https://example.com/repo", Path: "."},
		},
	}
}

func TestAppChangeFilter(t *testing.T) {
	f := NewAnnotationFilter([]string{"example.com/allowed"})

	t.Run("admits when either side is nil", func(t *testing.T) {
		assert.True(t, f.AppChangeFilter(nil, testApp("2", nil)))
		assert.True(t, f.AppChangeFilter(testApp("1", nil), nil))
	})

	t.Run("rejects a change confined to an ignored annotation", func(t *testing.T) {
		old := testApp("1", map[string]string{"example.com/noise": "a"})
		new := testApp("2", map[string]string{"example.com/noise": "b"})
		assert.False(t, f.AppChangeFilter(old, new),
			"editing an annotation that is never transferred must not produce an event")
	})

	t.Run("rejects adding an ignored annotation", func(t *testing.T) {
		old := testApp("1", nil)
		new := testApp("2", map[string]string{"argocd.argoproj.io/sync-wave": "5"})
		assert.False(t, f.AppChangeFilter(old, new))
	})

	t.Run("rejects removing an ignored annotation", func(t *testing.T) {
		old := testApp("1", map[string]string{"example.com/noise": "a"})
		new := testApp("2", nil)
		assert.False(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits an update that does not touch annotations", func(t *testing.T) {
		// The filter only ever suppresses annotation changes, so an update
		// whose annotations are untouched takes the fast path and is admitted
		// without the object being inspected further.
		old := testApp("1", map[string]string{"example.com/noise": "a"})
		new := testApp("2", map[string]string{"example.com/noise": "a"})
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits a change to a mirrored annotation", func(t *testing.T) {
		old := testApp("1", nil)
		new := testApp("2", map[string]string{common.AnnotationKeyAppSkipReconcile: "true"})
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits a change to an allow-listed annotation", func(t *testing.T) {
		old := testApp("1", map[string]string{"example.com/allowed": "a"})
		new := testApp("2", map[string]string{"example.com/allowed": "b"})
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits a change to the refresh annotation", func(t *testing.T) {
		old := testApp("1", nil)
		new := testApp("2", map[string]string{v1alpha1.AnnotationKeyRefresh: "normal"})
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits a spec change", func(t *testing.T) {
		old := testApp("1", nil)
		new := testApp("2", nil)
		new.Spec.Source.Path = "other"
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits a status change", func(t *testing.T) {
		old := testApp("1", nil)
		new := testApp("2", nil)
		new.Status.Sync.Status = v1alpha1.SyncStatusCodeSynced
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits a status change that also touches an ignored annotation", func(t *testing.T) {
		// The status change alone justifies the event; the annotation is
		// stripped from the payload separately.
		old := testApp("1", map[string]string{"example.com/noise": "a"})
		new := testApp("2", map[string]string{"example.com/noise": "b"})
		new.Status.Sync.Status = v1alpha1.SyncStatusCodeSynced
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits a deletion timestamp appearing", func(t *testing.T) {
		old := testApp("1", nil)
		new := testApp("2", nil)
		now := v1.Now()
		new.DeletionTimestamp = &now
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits an operation appearing", func(t *testing.T) {
		old := testApp("1", nil)
		new := testApp("2", nil)
		new.Operation = &v1alpha1.Operation{Sync: &v1alpha1.SyncOperation{}}
		assert.True(t, f.AppChangeFilter(old, new))
	})

	t.Run("admits a label change", func(t *testing.T) {
		old := testApp("1", nil)
		new := testApp("2", nil)
		new.Labels = map[string]string{"team": "platform"}
		assert.True(t, f.AppChangeFilter(old, new))
	})
}
