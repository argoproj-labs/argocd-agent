# Web-Based Terminal

This document explains how to enable and use the web-based terminal feature in argocd-agent, which allows users to get a shell inside a running pod on an agent cluster directly from the Argo CD UI.

## Overview

The web-based terminal provides `kubectl exec`-style access to pods running on remote managed clusters, accessible through the Argo CD UI. The terminal session is bridged from the browser through the principal to the agent via gRPC, and the agent executes into the target pod using the Kubernetes exec API.

```text
Browser ──WebSocket──► Argo CD Server ──HTTPS──► Principal ──gRPC──► Agent ──K8s Exec──► Pod
```

## Prerequisites

- The agent is running in managed mode.
- The resource-proxy is enabled on both the principal and agent.

## Enabling the Terminal

### Step 1: Enable the Exec Feature in Argo CD (Control Plane)

Set `exec.enabled` to `"true"` in the `argocd-cm` ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  exec.enabled: "true"
```

### Step 2: Configure Argo CD RBAC for Users (Control Plane)

Grant the `exec/create` permission to the appropriate roles in Argo CD's RBAC configuration. Add this to the `argocd-rbac-cm` ConfigMap or an `AppProject` manifest:

```text
p, role:myrole, exec, create, */*, allow
```

Users also need the `applications/get` permission on the target application.

### Step 3: Grant `pods/exec` Permission to the Agent (Workload Cluster)

The agent's ServiceAccount needs permission to exec into pods. Add the following to the agent's ClusterRole:

```yaml
- apiGroups:
  - ""
  resources:
  - pods/exec
  verbs:
  - create
```

## Using the Terminal

1. Open the Argo CD UI and navigate to an application deployed on a managed agent cluster
2. Click on a **Pod** resource in the application resource tree
3. Select the **Terminal** tab in the pod details panel
4. A shell session opens inside the selected container

## Changing Allowed Shells (Control Plane)

By default, Argo CD tries these shells in order: `bash`, `sh`, `powershell`, `cmd`. If the first shell is not found in the container, it falls back to the next one.

To customize the allowed shells, set the `exec.shells` key in the `argocd-cm` ConfigMap on the control plane:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  exec.enabled: "true"
  exec.shells: "bash,sh"
```

## Related Documentation

- [Argo CD Web-Terminal Documentation](https://argo-cd.readthedocs.io/en/stable/operator-manual/web_based_terminal/) - Argo CD documentation for web-terminal configurations
- [Accessing live resources on workload clusters](live-resources.md) - Resource proxy configuration and RBAC setup
- [Web Terminal Architecture](../technical/web-terminal-workflow.md) - Technical architecture diagram
