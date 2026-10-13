# argocd-agent ✨

[![Integration tests](https://github.com/argoproj-labs/argocd-agent/actions/workflows/ci.yaml/badge.svg)](https://github.com/argoproj-labs/argocd-agent/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/argoproj-labs/argocd-agent)](https://goreportcard.com/report/github.com/argoproj-labs/argocd-agent)
[![codecov](https://codecov.io/gh/argoproj-labs/argocd-agent/graph/badge.svg?token=NP5UEU279Z)](https://codecov.io/gh/argoproj-labs/argocd-agent)

**Scale Argo CD across hundreds of clusters with a single pane of glass** 🌐

Imagine managing GitOps deployments across edge locations, multiple cloud providers, air-gapped environments, and remote sites—all from one central dashboard. argocd-agent makes this reality by extending Argo CD with a distributed architecture that brings the control plane to you, no matter where your workloads live.

## 🚀 Why argocd-agent?

**The Challenge**: Traditional multi-cluster Argo CD setups hit walls when scaling to hundreds of clusters, especially across unreliable networks, air-gapped environments, or edge locations.

**The Solution**: argocd-agent flips the script—instead of your control plane reaching out to remote clusters, lightweight agents reach back to a central hub. This "pull model" enables:

✅ **Massive Scale**: Manage thousands of applications across hundreds of clusters  
✅ **Network Resilience**: Works with intermittent connections, high latency, or restricted networks  
✅ **Edge-Friendly**: Perfect for IoT, retail, manufacturing, or remote deployments  
✅ **Air-Gap Ready**: Secure deployments that never expose cluster internals  
✅ **Cloud Agnostic**: Seamlessly span AWS, GCP, Azure, on-premises, and hybrid environments  

## 🎯 Perfect For

- **🏭 Manufacturing**: Deploy to factory floors and remote facilities
- **🛒 Retail**: Manage point-of-sale and in-store systems across locations  
- **🚢 Edge Computing**: IoT deployments, autonomous vehicles, ships, and remote sites
- **🏛️ Enterprise**: Multi-datacenter deployments with strict security requirements
- **☁️ Multi-Cloud**: Unified GitOps across different cloud providers
- **🔒 Air-Gapped**: Secure environments with restricted network access

## ⚡ Quick Start

Get up and running in minutes! Check out our [**Getting Started Guide**](https://argocd-agent.readthedocs.io/latest/getting-started/) for step-by-step instructions.

## 🏗️ How It Works

Think of argocd-agent as a **hub-and-spoke architecture** where agents reach back to the control plane:

```
    ┌─────────────────┐
    │  Control Plane  │  ← Your Argo CD UI and API
    │   (The Hub)     │     (No outbound connections needed!)
    └─────────────────┘
              ▲ ▲ ▲
              │ │ │
              │ │ │
┌─────────────┘ │ └─────────────┐
│               │               │
│               │               │
┌─────────┐ ┌─────────┐ ┌─────────┐
│ Agent 1 │ │ Agent 2 │ │ Agent N │  ← Each agent connects independently
│ AWS     │ │ Factory │ │ Edge    │     (Pull model - no inter-agent links!)
└─────────┘ └─────────┘ └─────────┘
```

**🎛️ Control Plane**: Your familiar Argo CD interface—manage everything from one place  
**🤖 Agents**: Lightweight components that reach out and connect to the hub  
**🔄 Smart Sync**: Agents pull configuration and push status updates automatically  

### Two Flavors, One Experience

**🎯 Managed Mode**: Perfect for centralized control  
- Deploy applications from your control plane to remote clusters
- Ideal for rolling out updates, managing configurations, and maintaining consistency

**🦾 Autonomous Mode**: Built for independence  
- Remote clusters manage their own applications (via GitOps)
- Control plane provides observability and monitoring
- Perfect for air-gapped or highly autonomous environments

Mix and match modes across your fleet - some clusters managed, others autonomous, all visible from one dashboard.

## 🌟 Key Features

### 🛡️ **Security First**
- **mTLS everywhere**: All communications are encrypted and authenticated
- **Zero trust**: Control plane never needs direct cluster access
- **Certificate-based auth**: Strong identity verification for every agent

### 🌐 **Network Resilient**
- **Intermittent connections**: Agents work offline and sync when possible
- **High latency tolerant**: Designed for satellite links, cellular, and unreliable networks
- **HTTP/1.1 compatible**: Works through corporate proxies and legacy infrastructure

### 📊 **Unified Observability**
- **Single pane of glass**: See all clusters, applications, and deployments in one view
- **Real-time status**: Health, sync status, and metrics from all environments
- **Live resources**: Inspect Kubernetes resources across your entire fleet

### ⚙️ **Operationally Friendly**
- **Lightweight**: Minimal resource footprint on remote clusters
- **Self-healing**: Agents automatically reconnect and recover
- **Easy upgrades**: Rolling updates without downtime

## 📝 Blogs & Presentations

- **[Scaling GitOps with Pull Architecture for Global Fleets](https://developers.redhat.com/articles/2026/09/24/scaling-gitops-pull-architecture-global-fleets)** - Red Hat engineers share performance benchmarks and real-world testing results across hundreds of clusters

Have you written a blog post or given a presentation about argocd-agent? We'd love to feature it here! Submit a PR to add your content to this list.

## 🚧 Current Status

Track our progress and vision in the [**milestones**](https://github.com/argoproj-labs/argocd-agent/milestones) on GitHub.

## 🤝 Join the Community

We're building argocd-agent together! Whether you're a GitOps veteran or just getting started, there are many ways to contribute:

**💬 Get Help & Share Ideas**
- [GitHub Discussions](https://github.com/argoproj-labs/argocd-agent/discussions) - Ask questions, share use cases
- [#argo-cd-agent](https://cloud-native.slack.com/archives/C07L5SX6A9J) on [CNCF Slack](https://slack.cncf.io/) - Real-time chat

**🛠️ Contribute**
- [Contributing Guide](https://argocd-agent.readthedocs.io/latest/contributing/) - Code, docs, and testing guidelines
- [Issue Tracker](https://github.com/argoproj-labs/argocd-agent/issues) - Bug reports and feature requests
- [Good First Issues](https://github.com/argoproj-labs/argocd-agent/labels/good%20first%20issue) - Perfect for newcomers

**📖 Learn More**
- [**Documentation**](https://argocd-agent.readthedocs.io/latest/) - Comprehensive guides and references
- [**Architecture Deep Dive**](https://argocd-agent.readthedocs.io/latest/concepts/components-terminology/) - Understanding the internals
- [**Configuration Guide**](https://argocd-agent.readthedocs.io/latest/configuration/) - Detailed setup instructions

## 🏢 Ready to Deploy?

**Get involved:**

- **💼 Deploy in your environment** - See our [getting started guide](https://argocd-agent.readthedocs.io/latest/getting-started/) for installation
- **📋 Add your organization** - Using argocd-agent? Add yourself to our [USERS.md](USERS.md) file
- **💡 Share feedback** - Help shape the future through [discussions](https://github.com/argoproj-labs/argocd-agent/discussions)
- **🤝 Contribute** - Check out our [contributing guide](https://argocd-agent.readthedocs.io/latest/contributing/)

Ready to get started? [**Jump into our getting started guide**](https://argocd-agent.readthedocs.io/latest/getting-started/) or [**start a discussion**](https://github.com/argoproj-labs/argocd-agent/discussions) to share your plans!

## 📜 License

argocd-agent is licensed under the [Apache License 2.0](LICENSE).

---

<div align="center">

**Built with ❤️ by the Argo community**

[**⭐ Star us on GitHub**](https://github.com/argoproj-labs/argocd-agent) | [**📖 Read the Docs**](https://argocd-agent.readthedocs.io/latest/) | [**💬 Join the Discussion**](https://github.com/argoproj-labs/argocd-agent/discussions)

</div>
