# Autonomous mode

## Overview of autonomous mode

In *autonomous mode*, the workload cluster is wholly responsible for maintaining its own configuration. As opposed to the [managed mode](./managed.md), all configuration is first created on the workload cluster. The agent on this workload cluster will observe creation, modification and deletion of configuration and transmit them to the principal on the control plane cluster.

The principal will then create, update or delete this configuration on the control plane cluster. Users can use the Argo CD UI, CLI or API to inspect the status of the configuration, but they are not able to perform changes to the configuration or delete it.

## Architectural considerations

## Why chose this mode

## Why not chose this mode