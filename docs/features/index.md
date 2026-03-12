# Features Overview

argocd-agent transforms traditional multi-cluster Argo CD deployments by inverting the connection model: instead of a central control plane reaching out to remote clusters, lightweight agents establish connections back to the hub. This architectural shift enables GitOps at scale across distributed, unreliable, and restricted network environments.

## Current Capabilities

### Core Architecture

**Distributed Compute Model**: Application controllers run locally on workload clusters, reducing control plane load and improving resilience. Each cluster can scale and tune its application controller independently based on local requirements, eliminating the need for complex sharding configurations. The control plane maintains the familiar Argo CD UI and API while agents handle local reconciliation.

**Pull-Based Connectivity**: Agents initiate all connections to the control plane, eliminating the need for the control plane to have direct network access to workload clusters. This enables deployment across NAT boundaries, firewalls, and air-gapped environments.

**Vanilla Argo CD Integration**: Works with standard Argo CD installations without requiring custom forks or patches. Components can be deployed in various configurations depending on your scalability and availability requirements.

### Operational Modes

**[Managed Mode](../concepts/agent-modes/managed.md)**: Applications are defined on the control plane and distributed to agents. Ideal for centralized governance and policy enforcement across multiple clusters.

**[Autonomous Mode](../concepts/agent-modes/autonomous.md)**: Applications are defined locally on workload clusters and synchronized back for observability. Perfect for edge deployments, air-gapped environments, or scenarios requiring local autonomy.

### Communication Protocol

**gRPC with CloudEvents**: Efficient bi-directional communication using industry-standard protocols. The connection model supports intermittent connectivity and automatic reconnection.

**mTLS Security**: All communication is secured with mutual TLS authentication. Agents authenticate to the principal using client certificates, eliminating the need for the control plane to store cluster credentials.

**Pluggable Authentication**: Extensible authentication framework supporting mTLS and header-based methods out of the box. SPIFFE identity is supported through Envoy/Istio service meshes; first-class native SPIFFE support is still planned.

**[OpenTelemetry Tracing](../operations/tracing.md)**: Distributed tracing support using OpenTelemetry with OTLP export. Provides visibility into gRPC communication, resource synchronization, event processing, and Kubernetes operations across the distributed architecture.

### Resource Management

**Application Synchronization**: Full lifecycle management of Argo CD Applications, including creation, updates, deletion, and status reporting across the distributed architecture.

**AppProject Distribution**: Basic synchronization of AppProjects with mode-specific behavior. Managed agents receive projects from the control plane, while autonomous agents publish their projects for central visibility.

**Live Resource Access**: Transparent proxying of Kubernetes API requests to workload clusters through the control plane, enabling direct resource inspection and manipulation from the central Argo CD interface despite the distributed architecture.

**Custom Resource Actions**: Full support for executing Argo CD resource actions on workload clusters, allowing custom operations and workflows to be triggered from the central control plane.

**[ApplicationSet Support](../user-guide/applicationsets.md)**: ApplicationSets can run on either the control plane or the agent, with generated Applications synced through the normal agent protocol. In Managed mode, destination-based agent mapping enables ApplicationSets to generate Applications that are routed to the appropriate agents according to their target cluster destinations. ApplicationSet resources themselves are not synced between principal and agent, as the architecture operates at the Application level.

**Terminal Access**: Web-based terminal access to pods running on remote clusters, accessible directly from the Argo CD UI. The principal bridges WebSocket connections from the browser to agents via gRPC, and agents execute into pods using the Kubernetes exec API.

**Log Streaming**: Access pod logs from remote clusters through the control plane, enabling centralized log viewing from the Argo CD UI without direct cluster access.

### Management Tools

**argocd-agentctl CLI**: Command-line tool for managing agent configurations, certificates, and troubleshooting connectivity issues.

**Pluggable Backends**: Extensible storage backend architecture with Kubernetes as the default implementation, designed to support alternative storage solutions for large-scale deployments.

## Development Status

argocd-agent is in active development. Current functionality provides a solid foundation for the distributed GitOps vision, but several key features are still under development.

### Known Limitations

- **High Availability**: Principal component does not yet support high availability configurations
- **Advanced RBAC**: Multi-tenancy and advanced role-based access control features are still being developed

## Development Roadmap

### Near and Mid-term

- [Desired manifest access](https://github.com/argoproj-labs/argocd-agent/issues/344) for better debugging
- [First-class SPIFFE authentication](https://github.com/argoproj-labs/argocd-agent/issues/345) via the SPIFFE Workload API

### Long-term Vision

**Scalability Enhancements**

- [High availability](https://github.com/argoproj-labs/argocd-agent/issues/186) for the principal component
- Alternative storage backends for massive scale deployments
- Advanced load balancing and sharding strategies

**Enterprise Features**

- Comprehensive multi-tenancy support
- Advanced RBAC and policy enforcement

## Getting Involved

Development happens in the open on [GitHub](https://github.com/argoproj-labs/argocd-agent). We track all features, bugs, and enhancements in our [issue tracker](https://github.com/argoproj-labs/argocd-agent/issues) and organize them into [milestone releases](https://github.com/argoproj-labs/argocd-agent/milestones).

The project welcomes contributions from the community, whether in the form of code, documentation, testing, or feedback from real-world deployments. Join the conversation in [GitHub Discussions](https://github.com/argoproj-labs/argocd-agent/discussions) or the [#argo-cd-agent](https://cloud-native.slack.com/archives/C07L5SX6A9J) Slack channel.