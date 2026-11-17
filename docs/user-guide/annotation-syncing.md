# Application annotation syncing

Not every annotation on an Argo CD `Application` should travel between the
principal and an agent. Some describe state the source of truth owns, some are
argocd-agent's own bookkeeping, and some are meaningless outside the cluster
they were set on. argocd-agent classifies each annotation and applies the
behaviour that class requires.

## The three behaviours

### Source of truth to peer

The peer's copy of the annotation is wholly owned by the source of truth: the
**principal** for a managed agent, the **agent** for an autonomous one.

- Added or updated on the source of truth: added or updated on the peer.
- Deleted on the source of truth: deleted on the peer.
- Changed directly on the peer: reverted.

These annotations follow it:

| Annotation | Purpose |
|---|---|
| `notice.argocd.argoproj.io/*` | User-visible notices on an Application |
| `argocd.argoproj.io/manifest-generate-paths` | Narrows which repository paths trigger manifest generation |
| `argocd.argoproj.io/skip-reconcile` | Tells the application controller to skip the Application |

### Ignored

Any annotation argocd-agent does not recognise is left alone:

- It is **not** transferred to the peer.
- It is **not** included in the events sent over the wire. Editing one does not
  generate an event at all.
- It is **never** reverted. An annotation you set on an agent's Application
  stays exactly as you set it, and survives unrelated updates from the
  principal.

This is what lets another controller — or you, by hand — annotate an
Application on either side without argocd-agent fighting it.

These Argo CD annotations are ignored deliberately, because they are only
meaningful on a *child* Application inside an app-of-apps, and argocd-agent
never transfers a parent app-of-apps between control planes:

- `argocd.argoproj.io/compare-options`
- `argocd.argoproj.io/hook`
- `argocd.argoproj.io/hook-delete-policy`
- `argocd.argoproj.io/sync-options`
- `argocd.argoproj.io/sync-wave`
- `argocd.argoproj.io/ignore-resource-updates`

### Preserved

argocd-agent's own functional annotations — `argocd.argoproj.io/source-uid`,
`argocd.argoproj.io/principal-uid` and friends, and everything under
`argocd-agent.argoproj.io/` — are carried over rather than mirrored, and are
never removed by the generic merge. They identify the resource and drive
synchronization, so they are not something a user configures.

## The refresh annotation

A refresh is a one-shot *request* rather than state: something sets it, the
Argo CD application controller acts on it, and then removes it. Argo CD writes
two annotations for a single request, and argocd-agent special-cases both, in
both directions and in both agent modes:

- `argocd.argoproj.io/refresh`
- `argocd.argoproj.io/refresh-timestamp`

The behaviour is:

- Set a refresh on either side and it reaches the peer, whose application
  controller performs it.
- The controller removes the annotations once it has refreshed, and that
  removal propagates back, so neither side is left with a stale request.

They cannot be added to the allow list, because argocd-agent manages their
lifecycle itself.

## Allowing additional annotations

To make annotations belonging to another controller travel — the usual case is
[argocd-image-updater](https://github.com/argoproj-labs/argocd-image-updater) —
add them to the allow list. Allow-listed keys then follow the standard source of
truth to peer behaviour above.

Set the key in the `argocd-agent-params` ConfigMap on **both** the principal and
the agent. The value is a comma-separated list of glob patterns.

On the principal:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
data:
  principal.application.annotations.allow-list: "argocd-image-updater.argoproj.io/*"
```

On each agent:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-agent-params
data:
  agent.application.annotations.allow-list: "argocd-image-updater.argoproj.io/*"
```

This key is applied at runtime: editing it takes effect without restarting the
component. A present but empty value switches the allow list off, which is the
only way to undo a list configured by flag without a restart.

The equivalent flags and environment variables are
`--application-annotations-allow-list`,
`ARGOCD_PRINCIPAL_APPLICATION_ANNOTATIONS_ALLOW_LIST` and
`ARGOCD_AGENT_APPLICATION_ANNOTATIONS_ALLOW_LIST`.

!!! warning "Keep the two sides identical"
    The principal and each agent evaluate their own copy of the allow list.
    If they disagree, an annotation will be stripped by whichever side does not
    allow it, and the behaviour will look one-directional. argocd-agent does not
    reconcile the two lists for you.

### Patterns that are rejected

Unrecognised annotations are exactly what the allow list is for, so any of them
may be listed. What a pattern may **not** capture is:

- the preserved annotations, including `argocd.argoproj.io/source-uid` and the
  refresh annotations — transferring these would let a peer overwrite identity
  or control state and break synchronization
- `argocd.argoproj.io/tracking-id`, which tells Argo CD which instance owns a
  resource
- `kubectl.kubernetes.io/last-applied-configuration`, which embeds a copy of
  the object including its original namespace
- the six app-of-apps annotations listed under
  [Ignored](#ignored), which are meaningless on the peer by definition

A rejected pattern is logged and skipped; the rest of the list still applies,
and the component keeps running, so a typo cannot take it down.

A broad pattern such as `argocd.argoproj.io/*` or `*` is therefore rejected
outright, because it would capture `argocd.argoproj.io/source-uid` among
others. List the specific prefixes you need instead.

## Why unknown annotations are not synced

Before this behaviour existed, argocd-agent copied the whole annotation map
between the two sides. That had two consequences worth knowing about if you are
upgrading:

- Annotations that are meaningless on the peer were propagated anyway.
- An annotation set locally on one side was silently destroyed the next time the
  source of truth updated the Application for any reason — including a change
  that had nothing to do with annotations.

If you were relying on an arbitrary annotation being copied across, add it to
the allow list.
