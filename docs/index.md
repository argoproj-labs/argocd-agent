# argocd-agent - Multi-cluster agent for Argo CD

**Distributed GitOps at Scale**

argocd-agent reimagines multi-cluster GitOps by flipping the traditional architecture on its head. Instead of a central control plane struggling to reach out to hundreds of remote clusters, lightweight agents establish secure connections back to a unified hub. This fundamental shift unlocks GitOps deployment patterns that were previously impossible or impractical.

## The Challenge

Traditional Argo CD multi-cluster deployments face inherent limitations as they scale:

- **Network Complexity**: Control planes must maintain direct connections to every managed cluster
- **Security Boundaries**: Clusters behind NATs, firewalls, or in air-gapped environments are difficult to reach
- **Operational Overhead**: Complex sharding, credential management, and network troubleshooting
- **Single Points of Failure**: Centralized application controllers create bottlenecks and reliability risks

These challenges become exponentially more complex when managing dozens or hundreds of clusters across different networks, cloud providers, and security domains.

## The Solution

argocd-agent introduces a **pull-based architecture** where agents on workload clusters initiate connections to a central control plane. This inversion eliminates most traditional multi-cluster pain points while preserving the familiar Argo CD experience.

**Key Principles:**

- **Agents Connect Inward**: No outbound connections from the control plane
- **Local Reconciliation**: Application controllers run where workloads live
- **Unified Observability**: Single pane of glass for all clusters and applications
- **Standard Argo CD**: Works with vanilla Argo CD installations

See our [Features Overview](./features/index.md) for detailed capabilities and current limitations.

## Project Status

argocd-agent is in active development. It already demonstrates the viability of distributed GitOps at scale. The core architecture is stable, and essential features like application synchronization, live and desired resource access, and dual operational modes are working. We are not yet feature complete, but we encourage anyone to try and integrate argocd-agent for their use cases.


**We Need Your Help**

This project thrives on community involvement. Whether you're a GitOps veteran or just curious about distributed architectures, there are many ways to contribute:

- **Test and Experiment**: Try argocd-agent in development environments and share your experience
- **Report Issues**: Help us identify bugs, edge cases, and missing features  
- **Contribute Code**: Implement new features, fix bugs, or improve performance
- **Improve Documentation**: Help make complex concepts more accessible
- **Share Use Cases**: Tell us about your multi-cluster challenges and requirements

Every contribution, no matter how small, helps shape the future of distributed GitOps.

## Getting Started

Ready to explore distributed GitOps? Start with our [Getting Started Guide](./getting-started/index.md) to set up a local environment, or dive into the [Architecture Overview](./concepts/architecture.md) to understand how everything fits together.

For existing Argo CD users considering migration, check out our [Migration Guide](./user-guide/migration.md) for a comprehensive transition strategy.

## Community and Support

The argocd-agent project lives and thrives through its community. We are deeply grateful for any contributions and participation, whether you're reporting bugs, suggesting features, improving documentation, or contributing code. Every voice matters in shaping the future of distributed GitOps, and we welcome contributors of all experience levels to join us in building something meaningful together.

- **[GitHub Discussions](https://github.com/argoproj-labs/argocd-agent/discussions)** - Ask questions, share ideas, and connect with other users
- **[#argo-cd-agent](https://cloud-native.slack.com/archives/C07L5SX6A9J)** on CNCF Slack - Real-time community chat
- **[Issue Tracker](https://github.com/argoproj-labs/argocd-agent/issues)** - Bug reports and feature requests
- **[Contributing Guide](./contributing/index.md)** - How to get involved in development

## Documentation Notes

This documentation is actively maintained but reflects a rapidly evolving project. Some sections may be incomplete or outdated as we prioritize development velocity. We welcome contributions to improve clarity, accuracy, and completeness.

We assume familiarity with Argo CD concepts, Kubernetes, and GitOps principles. If you're new to these technologies, consider reviewing the [Argo CD documentation](https://argo-cd.readthedocs.io/) first.

## License

argocd-agent is Free and Open Source software released under the [Apache 2.0 License](https://github.com/argoproj-labs/argocd-agent/blob/main/LICENSE).