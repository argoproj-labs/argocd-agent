# Profiling

The argocd-agent uses [pprof](https://github.com/google/pprof) for collecting
profiling data.

Profiling is **disabled by default but always reachable**: both components start
a pprof listener at startup — the principal on `localhost:6060`, the agent on
`localhost:6161` — but every endpoint returns `403` until profiling is switched
on.

Two settings control it:

| Setting | Meaning | Changed at runtime? |
|---|---|---|
| `<component>.pprof.port` | Where the listener binds. `0` starts no listener at all. | No — needs a restart |
| `<component>.pprof.enabled` | Whether that listener answers requests. | **Yes** |

## Turning profiling on

Both components watch the `argocd-agent-params` ConfigMap they already read
their startup parameters from. Principal and agent share it and are told apart
by the key prefix, so one object configures both:

```bash
kubectl patch configmap argocd-agent-params -n argocd --type merge \
  -p '{"data":{"principal.pprof.enabled":"true"}}'
```

The change takes effect within a few seconds, with no restart. Then reach the
endpoint through a port-forward — it is bound to loopback, so it is never
exposed on the pod network:

```bash
kubectl port-forward -n argocd deploy/argocd-agent-principal 6060:6060
go tool pprof http://localhost:6060/debug/pprof/heap
```

Set the key back to `"false"` when you are done. To opt out of the listener entirely, set `<component>.pprof.port` to `0`. Note that this also makes `<component>.pprof.enabled` inert: with no listener there is nothing to gate, and turning profiling on then does require a restart.
