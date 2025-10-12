# Argo CD Agent Visibility UI

## Summary

This proposal introduces a new **Agent Visibility UI** for Argo CD Agent.  
Its purpose is to provide a clear and intuitive way to visualize the **connection and synchronization status** of multiple **Agents** within the Argo CD web interface.

Currently, users can only inspect Agent information using the `argocd-agentctl` CLI, making it difficult to monitor multi-cluster environments or understand Agent connectivity at a glance.  
This proposal adds a **simple, visibility-focused UI** integrated into the existing Argo CD dashboard.

---

## Motivation

The Argo CD Agent project enables distributed GitOps by allowing multiple Agents — running across different clusters or namespaces — to connect to a single Principal.
While this architecture supports large-scale operations, the current lack of visual monitoring introduces key challenges:

- No unified view of all Agents connected to the Principal.
- Difficult to identify unreachable or disconnected Agents.
- No easy way to inspect Agent configurations such as mode, TLS setup, or Redis linkage.

A native visibility UI allows operators and developers to quickly assess cluster connectivity and inspect configuration details—all within Argo CD.

---

## Goals

- Introduce a new **“Agents”** navigation item in the Argo CD sidebar.
- Display **Principal & Agents** relationship overview.
- Provide **Tile, List, Tree, and Detail** views for clear visualization.
- Show key Agent data: connection, sync status, mode, namespace, and Principal linkage.
- Keep v1 focused on **simple visibility and clarity**, without complex interactions.

---

## Non-Goals

- Deploying, deleting, or reconfiguring Agents.
- Real-time metric streaming or Prometheus integration.
- Editing configuration fields within the UI.

---

## Proposal

### 1. Agents Overview Page

The **Agents** entry in the sidebar opens the overview page, which supports two layouts:  
**Tile View** and **List View**. Users can toggle between them.

#### Tile View

Each card represents either the **Principal** or an **Agent**.

**Principal Card Fields**

- **Name:** Principal name
- **Connecting Agents:** number of Agents currently linked
- **Listen Port / Redis Address:** summary of current operational endpoints

**Agent Card Fields**

- **Mode:** `Managed` or `Autonomous`
- **Connection Status:** `Connected`, `Unreachable`, `Disconnected`
- **Sync Status:** `Synced` or `OutOfSync`
- **Principal:** shows associated Principal name
- **Destination:** deployment location (e.g. `in-cluster`)
- **Server Address / Port:** connection endpoint if available
- **Actions:** `Refresh`

Color borders indicate connection health:

- 🟢 Green → Connected
- 🟡 Yellow → Unreachable
- 🔴 Red → Disconnected

#### List View

A compact version of the Tile View with rows for each Agent.

#### Principal Header Row

- **Name:** The name of the Principal (e.g., `agent-principal`)
- **Connecting Agents:** Number of Agents currently connected (e.g., `2`)

#### Agent Rows

Each Agent entry displays key configuration and status information:

- **Principal:** The associated Principal (always the same single Principal)
- **Name:** The Agent name (e.g., `agent-1`)
- **Mode:** `Managed` or `Autonomous`
- **Destination:** The deployment location (e.g., `in-cluster`)
- **Status (right side):**
  - **Connection:** `Connected`, `Unreachable`, or `Disconnected`
  - **Sync:** `Synced` or `OutOfSync`

#### Visual Cues

- Left border color indicates **connection** state:
  - 🟢 **Green** → Connected
  - 🟡 **Yellow** → Unreachable
  - 🔴 **Red** → Disconnected

#### Navigation

- Clicking an **Agent row** opens the **Agent Detail View**.
- Clicking the **Principal header row** (or its “Details” action) opens the **Principal Detail View**.

---

### 2. Agents Tree View

Displays a hierarchical **Principal → Agents** visualization.

- The Principal appears as the **root node** (e.g., `agent-principal`).
- Each Agent node branches from it, showing:
  - Connection (`conn:`) and Sync (`sync:`) indicators.
  - **Mode** (Managed / Autonomous) displayed as a small tag.
- Unreachable or disconnected Agents are visually dimmed or marked with warning icons.

Clicking an **Agent node** opens the **Agent Detail View**.  
Clicking the **Principal header row** (or its “Details” action) navigates to the **Principal Tree View**.

---

### 3. Detail Views

#### Principal Detail View

A configuration-oriented detail sheet showing all runtime parameters grouped by section:

**General**

- Namespace
- Allowed Namespaces
- Log Level / Format

**Network**

- Listen Port
- Metrics Port
- Health Check Port
- gRPC / Proxy Ports

**Namespace Management**

- Auto-Create Namespace
- Create Pattern / Create Labels

**Authentication / TLS**

- Authentication Method
- Require Client Cert
- TLS Secret / Key Paths
- Root CA Path
- Mutual Subject Mapping

**JWT / Token**

- JWT Key Paths and Secrets

**Resource Proxy**

- Enabled
- TLS Secret
- Cert / Key Paths

**Redis**

- Redis Server Address
- Compression Type

**Communication**

- Keep-Alive Interval
- WebSocket Enabled

#### Agent Detail View

Displays the specific Agent’s runtime config and link to its Principal.

**General**

- Mode (`Managed` / `Autonomous`)
- Namespace
- Log Level / Format

**Connection (Principal)**

- Principal Address
- Principal Port
- Credentials

**Network**

- Metrics Port
- Health Check Port
- Proxy Port

**TLS / Security**

- Skip TLS Validation
- Root CA Secret Name
- TLS Secret Name (Agent Cert)
- Client Cert / Key Paths

**Resource Proxy**

- Enabled

**Redis**

- Redis Server Address

**Communication**

- gRPC Compression Enabled
- Keep-Alive Interval
- WebSocket Enabled

---

## Use Cases

**Use case 1:**  
As an operator, I want to visually confirm which Agents are connected or unreachable, directly in Argo CD.

**Use case 2:**  
As a developer, I want to quickly check the synchronization and connection status for each Agent.

**Use case 3:**  
As an administrator, I want to review Principal and Agent configurations side by side for debugging or validation.

---

## Implementation Details

- The **UI** will be implemented within the Argo CD React/TypeScript frontend.
- The **API server** will expose new endpoints for:
  - Listing Agents and their connection metadata.
  - Returning full Principal and Agent configuration payloads.
- Data source: Argo CD’s internal cache or the `argocd-agent` control-plane API.

---

## Security Considerations

This proposal only surfaces existing configuration and status data.  
No additional privileges or mutating endpoints are required.

---

## Risks and Mitigations

| Risk                        | Mitigation                                   |
| --------------------------- | -------------------------------------------- |
| High Agent count impacts UI | Implement pagination and lazy rendering      |
| Outdated cache data         | Add manual “Refresh” button for quick reload |

---

## Upgrade / Downgrade Strategy

This change introduces only additional UI components and supporting APIs.  
Existing Argo CD instances remain unaffected.  
If no Agents are registered, the “Agents” section will show a neutral empty state.

---

## Future Work

- Provide Agent action controls such as **Reconnect**, **Restart**, and **Sync**
- Add a UI to link and navigate between **Applications** and their managing **Agents**
- Allow editing of **Agent configuration** directly from the Argo CD UI
