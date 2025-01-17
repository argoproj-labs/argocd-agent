# Synchronization of AppProjects

## Meta

|||
|---------|------|
|**Title**|Synchronization of AppProjects|
|**Document status**|Work in Progress|
|**Document author**|@jannfis|
|**Implemented**|No|

## Abstract

Argo CD's `AppProjects` are entities that traditionally exist within Argo CD's installation namespace and should only be modified by administrators. Mapping an agent as the synchronization target for an `Application` is rather straight forward, as we leverage apps-in-any-namespaces on the control plane and then map each agent to a certain namespace. However, for `AppProjects` this is not possible, so we need to find another strategy to map `AppProjects` to a synchronization target (i.e. agent). 

## Requirements

* An AppProject must be usable on both, the control plane and the agent managed system. The API/UI on the control plane will enforce restrictions set out in an AppProject for display and management purposes, and the remote system will use the same AppProject to determine how and if Applications are going to be reconciled.
* An AppProject must be usable on more than one remote cluster (i.e. a n:m mapping), so DRY patterns can be applied while defining AppProjects and unneccessary complexity in configuration can be avoided.
* AppProject naming need to be unique on the control-plane. 

## Assumptions

The following assumptions are underlying to the design:

* AppProjects are not constantly updated: Determining the relationship between an AppProject and agents could potentially be compute expensive, especially if wildcard patterns are being used in `.spec.sourceNamespaces`.
* AppProjects are configured only by Argo CD administrators: Again, due to the expensive nature of mapping an AppProject to an agent, we assume that AppProjects are only ever configured by administrators (or similarly high privileged parties).

## Design

The idea is to leverage the `.spec.sourceNamespaces` field to determine the synchronization targets. This field needs to be set in order to allow Applications to be displayed by the UI, and a source namespace is already mapped to an agent. So when a namespace is listed in `.spec.sourceNamespace`, it additionally defines the synchronization target: The agent that is synchronized from this namespace.

Although this seems to make the field `.spec.sourceNamespaces` ambigiuous at first, a closer look shows that it makes sense - because a namespace on the control plane defines the mapping, and in order for an Application to be manageable, it must be associated to an AppProject which already contains that namespace in the `.spec.sourceNamespaces`.

Using the `.spec.sourceNamespaces` field also allows for n:m relationships between AppProjects and managed clusters.

### On principal startup

When the principal starts up, it lists all currently existing AppProjects on the control plane as part of the informer setup and configures the watch.

As no agents can be connected until the informer is ready, no further action is required at this point.

### On agent connection

Once an agent connects to the principal, its AppProjects need to be synchronized with the control plane first. This needs to be a two-way sync:

* AppProjects that exist on the agent but do not exist on the control plane anymore, or are not anymore mapped to a given agent, need to be deleted
* AppProjects that are mapped to the agent need to be upserted on the agent, that is, those that do not exist on the agent need to be created and those that do exist need to be updated with the latest configuration from the control plane.

### On AppProject creation

When a new AppProject is created on the control plane, `argocd-agent` will evaluate the new object's `.spec.sourceNamespaces` field to determine the connected synchronization targets. The `.spec.sourceNamespaces` field is a list of strings, so the principal has to iterate over this list to find all potential targets.

The new AppProject is then submitted to all connected agents for creation on their respective systems.

If an agent that would be the target of an AppProject creation is not connected at the time of AppProject creation, it will receive the create request the next time it connects to the principal.

### On AppProject modification

When an AppProject is modified on the control plane, the informer provides information about the AppProject before and after the modification. The principal then needs to compare the `.spec.sourceNamespaces` field of both versions and apply the following logic:

* For each entry that exist in the old resource version, but not in the new one, the control plane needs to submit a deletion request to the connected agents matching this entry
* For each entry that exist in the new resource version, but not in the new one, the control plane needs to submit a creation request to the connected agents matching this entry
* For all other entries in the new resource version, an update request must be sent to the connected agents matching these entries

If an agent that would be the target of an AppProject modification is not connected at the time of AppProject modification, it will receive either of the create, delete or modification request the next time it connects to the control plane.

### On AppProject deletion

When an AppProject is deleted on the control plane, the informer provides information about the deleted AppProject. For each of the entries in the `.spec.sourceNamespaces` field, the principal submits a deletion request for this AppProject to the connected agents matching the given pattern.

If an agent that would be the target of an AppProject deletion is not connected at the time of AppProject deletion, it will receive the deletion request the next time it connects to the principal.

## Open questions

* Should AppProjects on remote clusters allow other remote clusters as deployment targets ("_fan-out_")?
* How to handle AppProjects with autonomous agents? Autonomous agents are supposed to only report back resources and their state to the control plane, so the control plane does not have governance about resource names. This can lead to conflicts.
* Should wildcard patterns be allowed in the `.spec.sourceNamespaces` field, as a matching algorithm might potentially be expensive.


## Risks

## Further considerations