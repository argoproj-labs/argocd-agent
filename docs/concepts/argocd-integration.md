# Integration with Argo CD

## Overview

The *argocd-agent* is designed to complement, not replace, Argo CD by extending its capabilities to multi-cluster environments. It provides two distinct integration patterns that allow you to balance resource efficiency, operational autonomy, and resilience based on your specific requirements.

This integration enables centralized GitOps management while distributing the actual workload execution across multiple Kubernetes clusters. The agent acts as a bridge between your central control plane (where configuration and observability are managed) and your workload clusters (where applications are deployed and managed).

### Diagram Legend

In the diagrams throughout this document:

* **Light green boxes** represent *argocd-agent* components
* **Light blue boxes** represent Argo CD components  
* **Light red boxes** represent external systems and components

Components with dotted outlines indicate their deployment location depends on the selected [operational mode](./agent-modes/index.md) of the agent.

!!! warning "Integration Pattern Selection"
    The choice between integration patterns is a fundamental architectural decision that affects your entire GitOps infrastructure. While agents can operate in different modes within the same environment, all workload clusters must use the same integration pattern. Switching between patterns requires service interruption and careful migration planning.

## Integration patterns

### Pattern 1: Centralized Resource Sharing (Low Footprint)

This integration pattern centralizes critical Argo CD components on the control plane cluster while minimizing the footprint on workload clusters. The *repository-server* and *redis-server* components are shared across all workload clusters, with only the *application-controller* deployed locally on each workload cluster.

![Integration pattern 1: Low footprint spokes](../assets/02-integration-shared.png)

In this architecture, each workload cluster runs only the *application-controller* (and optionally the *applicationset-controller* when operating in autonomous mode). These controllers communicate with the centralized *repository-server* and *redis-server* on the control plane cluster for manifest rendering and state caching.

**Advantages of this pattern**

* **Reduced resource consumption**: Workload clusters require minimal compute resources since heavy operations like Git repository processing and manifest rendering occur on the control plane
* **Simplified network requirements**: Workload clusters only need connectivity to the control plane cluster, eliminating the need for direct Git repository access
* **Centralized state management**: Application state and rendered manifests are stored centrally, enabling efficient API operations and reducing data duplication
* **Cost efficiency**: Fewer components per workload cluster translate to lower operational costs, especially beneficial for large numbers of clusters

**Disadvantages of this pattern**

* **Single point of failure**: The control plane cluster becomes critical infrastructure - if unavailable, workload clusters cannot render new manifests or update cached state, halting reconciliation processes
* **Network dependency**: Increased network traffic between workload and control plane clusters may create bottlenecks or increase costs in cloud environments with inter-zone/region charges
* **Centralized scaling challenges**: The *repository-server* and *redis-server* must be scaled to handle the aggregate load from all workload clusters, requiring careful capacity planning
* **Complex ingress management**: Additional network ingress points and credential management are required on the control plane for each workload cluster connection
* **Limited fault isolation**: Issues with shared components affect all connected workload clusters simultaneously 

### Pattern 2: Fully Autonomous Workload Clusters

This integration pattern deploys a complete Argo CD stack (*application-controller*, *repository-server*, and *redis-server*) on each workload cluster, creating fully autonomous GitOps environments. Each workload cluster operates as an independent Argo CD instance while maintaining centralized configuration management and observability through the control plane.

![Integration pattern 2: Autonomous spokes](../assets/02-integration-autonomous.png)

This architecture enables workload clusters to perform all GitOps operations locally, including Git repository access, manifest rendering, and state management. The control plane cluster serves primarily as a configuration hub and observability aggregation point.

**Advantages of this pattern**

* **True operational autonomy**: Workload clusters continue functioning independently during control plane outages, with only configuration updates and observability affected. When combined with [autonomous mode](./agent-modes/autonomous.md), clusters maintain full GitOps capabilities even during extended control plane unavailability
* **Reduced network traffic**: Minimal communication required between workload and control plane clusters, eliminating bandwidth bottlenecks and reducing inter-cluster network costs
* **Distributed scaling**: Each workload cluster scales its Argo CD components independently based on local requirements, enabling optimal resource utilization
* **Simplified networking**: Single ingress point required on the control plane cluster for agent communication, reducing network complexity and security surface area
* **Enhanced fault isolation**: Issues with one workload cluster's components don't affect other clusters in the environment
* **Improved performance**: Local processing of Git operations and manifest rendering eliminates network latency from the GitOps workflow

**Disadvantages of this pattern**

* **Increased resource requirements**: Each workload cluster must allocate compute resources for the full Argo CD stack, increasing the minimum viable cluster size
* **Git repository access**: Workload clusters require direct network connectivity to Git repositories, potentially complicating network security policies and firewall configurations
* **Distributed state management**: Application state is distributed across workload clusters, making centralized monitoring and troubleshooting more complex
* **Higher operational complexity**: Managing and maintaining Argo CD components across multiple clusters increases operational overhead
* **Resource duplication**: Git repository caching and manifest rendering occur independently on each cluster, potentially leading to redundant resource usage
* **Security considerations**: Each workload cluster needs credentials for Git repository access, expanding the credential management scope

## Recommendation

**We strongly recommend using Pattern 2 (Fully Autonomous Workload Clusters) for most production environments**, except when operating under severe compute resource constraints.

The autonomous pattern provides significant operational benefits that outweigh its resource overhead in most scenarios:

### Why Choose Autonomous Clusters

1. **Operational Resilience**: Your GitOps workflows continue functioning during control plane maintenance, upgrades, or unexpected outages. This is crucial for production environments where application deployments cannot be delayed.

2. **Performance and Reliability**: Eliminating network dependencies from the GitOps workflow reduces latency, potential network failures, and bandwidth costs. Local processing ensures consistent performance regardless of control plane load.

3. **Scalability**: Each cluster scales independently, avoiding the complex capacity planning required for centralized components that must handle aggregate loads from all workload clusters.

4. **Operational Independence**: Teams can manage their workload clusters with greater autonomy, reducing dependencies on central infrastructure teams.

### When to Consider Centralized Resource Sharing

Pattern 1 (Centralized Resource Sharing) may be appropriate in the following scenarios:

* **Resource-constrained environments**: Edge computing deployments, IoT clusters, or environments where compute resources are severely limited
* **Development and testing environments**: Where operational resilience is less critical than resource efficiency
* **Highly regulated environments**: Where centralized control and reduced attack surface are prioritized over operational autonomy
* **Small-scale deployments**: With fewer than 5-10 workload clusters where the operational overhead of distributed management outweighs the benefits

### Implementation Considerations

When implementing the autonomous pattern:

* Ensure workload clusters have adequate resources for the full Argo CD stack (typically requiring an additional 1-2 CPU cores and 2-4GB RAM)
* Plan for Git repository access from all workload clusters, including necessary network policies and credentials
* Implement monitoring and alerting for distributed Argo CD components
* Consider using [autonomous mode](./agent-modes/autonomous.md) to maximize resilience during control plane outages

The additional resource investment in autonomous clusters typically pays dividends through improved reliability, performance, and operational flexibility in production environments.