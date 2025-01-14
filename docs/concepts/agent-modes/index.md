# Agent modes

The main purpose of the [agent](../components-terminology.md#agent) and the [principal](../components-terminology.md#principal) components is to keep configuration in sync between the [workload clusters](../components-terminology.md#workload-cluster) and the [control plane cluster](../components-terminology.md#control-plane-cluster).

Each agent can operate in one of two distinct configuration modes: *managed* or *autonomous*. These modes define the general sync direction: From the workload cluster to the control plane cluster (*autonomous*), or from the control plane cluster to the workload cluster (*managed*).

Please refer to the sub-chapters [Managed mode](./managed.md) and [Autonomous mode](./autonomous.md) for detailed information, architectural considerations and constraints to chose the mode most appropriate for your agents.