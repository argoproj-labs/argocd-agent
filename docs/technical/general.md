# General architectural considerations

## Meta

|||
|---------|------|
|**Title**|General architectural considerations|
|**Document status**|Work in Progress|
|**Document author**|@jannfis|
|**Implemented**|No|

## Abstract

This document describes some of the general architectural considerations and design goals within the `argocd-agent` code base. We'd like every contributor to read this document and assess their own contributions against the information laid out in this doc.

## Runtime

### Process termination, fatalities

Program flow should not be terminated lightly. Instead, whenever you would terminate the program flow, it should be evaluated if the error situation is recoverable. For example, recoverable situations that are aborted in other programs usually are:

* A TCP server cannot listen on a given address/port.

### Logging

We make use of structured logging. As much information as possible should 

## Networking