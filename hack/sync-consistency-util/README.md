
# Application Synchronization Simulation and Verification Utility

The primary goal of this utility is to verify the correctness of Application synchronization behaviour between agent and principal. 

This utility simulates Application resource events:
- Randomly creates/modifies/deletes Argo CD Application resources on control plane cluster
    - Verifies that the corresponding creation/modification/delete was made on managed-agent, in order and in a timely fashion (via K8s watch API)
    - Each time Application is modified, the `.spec.source.repoURL` field is modified with a unique value, to track `.spec` synchronization to agent
- Randomly modifies `.status` field of Argo CD Applications on workload cluster
    - Verifies that the corresponding `.status` update is broadcast back to Application on control plane cluster
    - Each time Application status is modified, the `.status.sync.revision` field is modified with a unique value, allowing us to track `.status` synchronization back to principal
- `.spec` writer and `.status` writer run concurrently, from a single OS process
- `.spec` watcher and `.status` watcher (responsible for observing events before verification) likewise run concurrently

However, the implementation details of this utility are slightly more complex, due to Argo CD agent de-duplication behaviour:
- Event writer (`event_writer.go`) will de-dupe '.status update' and '.spec update' events 
- Event writer will also de-dupe delete events: if an application is deleted, all previously unsent events (.spec modifications, etc) will be discarded (as they are necessarily stale)
- If informer event buffer (`informer_event_buffer.go`) feature is enabled, updates to Application `.spec` that occur within X seconds will be de-duplicated

This complex behaviour requires that this utility uses an algorithm that checks for eventual consistency, rather than an algorithm that looks for exact match between source/destination events.


## How to run


#### Setup

Test utility currently assumes standard vcluster dev-env.

```
make setup-e2e
```

#### From window A: start agent processes
Start principal/agents on vcluster dev-env, running locally:
```
make start-e2e

# If you want to write the screen output to a file for debugging purposes, use `script (filename)`.

# or, if debugging an eventual consistency problem, you can enable full logging on agent via:
ARGOCD_AGENT_FULL_DETAIL=all ARGOCD_AGENT_LOG_LEVEL=trace  ARGOCD_PRINCIPAL_FULL_DETAIL=all ARGOCD_PRINCIPAL_LOG_LEVEL=trace make start-e2e
```

#### From window B: start utility

In a separate window, run the utility:
```
cd hack/sync-consistency-util

# Modify constants in `main.go` to fit your needs, or just run default.

go run .

# Or, if you want to keep running it over and over until it fails (useful for finding bugs), see the 'until-fail.sh' utility (from https://gist.github.com/jgwest/7048a765d398519837f990120cf3fdd0) and use:

until-fail.sh go run .
```

Note:
- Running this utility will delete all Applications on all vclusters at startup.
- This will set replicas to 0 on application controller on managed agent (to avoid generating extra events).


## Further details

See source code for implementation details of eventual consistency checking.

Principles of eventual consistency:
- There is a non-trivial delay between when a create/update/delete is made on source (of truth), to when it is transmitted, processed, and applied on destination peer
    - Within the utility codebase we refer to this non-trivial delay as propagation delay. 
    - The more overloaded the K8s API server/etcd, and agents, the longer this propagation delay can take.
    - We thus allow configuration of a 'max propagation delay' constant, which is the max expected value before we assert an event has been incorrectly, permanently lost
- Add/delete events (lifecycle events) must always be transmitted from source/destination, as there should be no-dedupe of these:
    - With one exception: if an application is created then quickly deleted, e.g. the outbox will contain: '1) create Application event 2) delete Application event'
    - In this case, when the outbox still contains both, neither will be sent. (There is no point to sending a create if you already know it will be deleted immediately)
- However, not all update events will be transmitted from source to destination: some deduplication occurs within agent code at various points
    - BUT, even though we are de-duplicating spec-update and status-updates, we should never permanently MISS an Application resource update: 
        - If
            - Event 'A' occurs on source of truth (SoT) and is processed on peer (non-SoT) at time t=X
            - And, one (or more) event 'B'-'Z' occur (on SoT) at T=X+Y, for some Y > 0
        - Then
            - At least one of the events from 'B'-'Z' should be processed on peer by X+'PD' (where PD is expected propagation, e.g. 15 seconds).
- We should never receive events out of the order the event occurred on source of truth:
    - A delete sent before a create
    - Updates received in a different order on peer, from the order they were sent from source of truth

## Current Limitations

These are not design limitations: these are just features not yet implemented.

- `Application` resources only (no other resource types are validated via create/modify/delete)
- Non-destination-based-mapping case only (consistent with `make setup-e2e` default, as of this writing)
- Assumes/requires that we are running on local dev-env/vcluster configuration (via `make setup-e2e`)
- Only 1 writer goroutine per writer type (one for spec, one for status) -- spec-writes and status-writes run concurrently, but there are no parallel writers of the same type
- Currently only verifies principal <-> managed-agent. Does not verify principal <-> autonomous-agent.
- This tool may also be useful for benchmarking, BUT, at present we are blocked on vcluster etcd CPU usage 
- Simulate creation of a new `Application` with name of a previously deleted `Application:` The mechanism I am using to create Applications will never create an application which had the name of a previously deleted application.
- Currently this utility works end to end: a single OS process uses client-go to both watch Application CR and also create/update/delete Application CR. Instead, this could be split into two separate utilities, with a third utility to check the results. This would be useful for large scale multi-cluster (not just vcluster) checking.

## Outstanding improvements to Argo CD Agent
- As of this writing, boundedQueue [will silently drop events](https://github.com/argoproj-labs/argocd-agent/blob/8241768ae7a0ed5d110187af83d40b2fbf988f81/internal/queue/queue.go#L71)