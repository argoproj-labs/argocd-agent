# Synchronization of Applications

## Meta

|||
|---------|------|
|**Title**|Synchronization of Applications|
|**Document status**|Work in Progress|
|**Document author**|@jannfis|
|**Implemented**|Partly|

## Abstract

An `Application` is the at heart of Argo CD. In a nutshell, it maps one or more source(s) from Git (or another type of repository) to a destination cluster and specifies how to reconcile it. The majority of the reconciliation work is performed by Argo CD's application controller. 

There are generally two parts to an Application resource:

* the `.spec` field, which holds user configurable reconciliation settings
* the `.status` field, where the application controller records a variety of information regarding the reconciliation activity

The status field is usually written to by the application controller, where as the spec can be written to by the user (e.g. through kubectl, Argo CD CLI or the web UI).

A third field, `.operation`, can be written by both, the user and the controller. It is attached to the resource when a sync operation is requested, and removed from the resource once the operation is in progress.

## Requirements

* An Application must only be reconciled on the target system, not on the control plane
* Whenever an Application is transfered from the principal to the agent or vice versa, it must be ensured 

## Assumptions

## Design

## Open questions

## Risks

## Further considerations
