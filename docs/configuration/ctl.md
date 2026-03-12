# argocd-agentctl

This document will explain how to use argocd-agentctl to assist in management of argocd-agent components.

## Available Commands

+These are the available commands for argocd-agentctl. Some commands have subcommands, which are detailed in the sections below.

`agent` - Inspect and manage agent configuration

`check-config` - Validate principal and agent configurations by running a series of tests

`config` - Operations related to config file for argocd-agentctl

`jwt` - Inspect and manage JWT signing keys

`pki` - Inspect and manage the principal's PKI **(NOT FOR PRODUCTION USE)**

## Global Flags

The tool supports the following global flags for specifying which principal or agent cluster to target.
They are as follows:

```
--config string                The path to the local config file to use (default "$HOME/.config/argocd-agent/ctl.conf")

--agent string                 The symbolic name of an agent in the config file
--agent-context string         The Kubernetes context of the agent
--agent-namespace string       The Kubernetes namespace the agent is installed in (default "argocd")

--principal string             The symbolic name of a principal in the config file
--principal-context string     The Kubernetes context of the principal
--principal-namespace string   The Kubernetes namespace the principal is installed in (default "argocd")
```

Below is the priority of flags for both the agent and principal:

**Agent:**

(Highest Priority)

1. `--agent-context` and  `--agent-namespace` flags
2. `--agent` flag
3. Current context selected in kubeconfig in `argocd` namespace (no flags or default provided)

(Lowest Priority)

**Principal:**

(Highest Priority)

1. `--principal-context` and  `--principal-namespace` flags
2. `--principal` flag
3. Default Principal in Config
4. Current context selected in kubeconfig in `argocd` namespace (no flags or default provided)

(Lowest Priority)

## Local Configuration

argocd-agentctl supports a local configuration file to enhance ease of use.
This file lets you refer to principals/agents by a symbolic name when calling the command line,
stopping you from needing to provide the `--principal/agent-context` and `--principal/agent-namespace` flags.

The format for the config is below. You can either copy and paste this to the default path at `$HOME/.config/argocd-agent/ctl.conf`
or use the `config create` command to generate a config file from your kubeconfig.

It also supports setting a default principal that will be selected when you do not provide any flags.

Below is the format of the config file.

```
contexts:
    principals:
        hub:
            kube-context: hub-context
            namespace: argocd
    agents:
        spoke-a:
            kube-context: spokea-context
            namespace: argocd
        spoke-b:
            kube-context: spokeb-context
            namespace: argocd
default-principal: hub
```

## `agent` Command

Inspect and manage agent configuration

**Subcommands:**

`create` - Create a new agent configuration

`inspect` - Inspect agent configuration

`list` - List configured agents

`print-tls` - Print the TLS client certificate of an agent to stdout

`reconfigure` - Reconfigures an agent's properties

## `check-config` Command

Validate principal and agent configurations

**Subcommands:**

`agent` - Validate agent configuration (and principal cross-checks)

`principal` - Validate principal configuration

## `config` Command

Operations related to config file for argocd-agentctl

`add` - Add new entries to the local config file

`create` - Creates and populates a local user config file for Argo CD Agent by parsing the kubernetes config file

`delete` - Delete entries from the local config file

`edit` - Edit entries in the local config file

`list` - List components in the config

## `jwt` Command

The jwt command provides functions to create, inspect and manage JWT signing keys for argocd-agent
principal. JWT signing keys are used by the principal to sign authentication tokens for agents.

**Subcommands:**

`create-key` - Create a JWT signing key and store it in a Kubernetes secret

`delete-key` - Delete the JWT signing key secret

`inspect-key` - Inspect the JWT signing key

## `pki` Command **(NOT FOR PRODUCTION USE)**

The pki command provides functions to inspect and manage a public key infrastructure
(PKI) for argocd-agent. Its whole purpose is to get you started without having
to go through hoops with a real CA.

DO NOT USE THE PKI OR THE CERTIFICATES ISSUED BY IT FOR ANY SERIOUS PURPOSE.
DO NOT EVEN THINK ABOUT USING THEM SOMEWHERE IN A PRODUCTION ENVIRONMENT,
OR TO PROTECT ANY KIND OF DATA.

`delete` - Delete the configured PKI

`dump` - Print PKI's CA data in PEM format to stdout

`init` - NON-PROD!! Initialize the PKI for use with argocd-agent

`inspect` - NON-PROD!! Inspect the configured PKI

`issue` - NON-PROD!! Issue TLS certificates signed by the PKI's CA

`propagate` - NON-PROD!! Propagate the PKI to the agent

