# Configuration Reference

This section provides complete reference documentation for all configuration parameters of the argocd-agent principal and agent components.

## Reference Pages

| Component | Description |
|-----------|-------------|
| [Principal Parameters](principal.md) | All configuration parameters for the principal component |
| [Agent Parameters](agent.md) | All configuration parameters for the agent component |

## How to Use This Reference

Each parameter entry includes:

- **Command Line Flag**: The flag to use when running the binary directly
- **Environment Variable**: The environment variable name for container deployments
- **ConfigMap Entry**: The key to use in the `argocd-agent-params` ConfigMap
- **Description**: What the parameter does
- **Type**: The data type (string, integer, boolean, duration, etc.)
- **Default**: The default value if not specified
- **Example**: Example usage

## Configuration Precedence

When a parameter is set via multiple methods, the following precedence applies:

1. **Command line flags** (highest precedence)
2. **Environment variables**
3. **ConfigMap entries** (lowest precedence)

## Quick Links

### Principal Configuration

- [Server Configuration](principal.md#server-configuration) - Listen host, port settings
- [TLS Configuration](principal.md#tls-configuration) - TLS certificates and settings
- [Authentication](principal.md#authentication-configuration) - Auth methods
- [Logging](principal.md#logging-and-debugging) - Log level, format
- [Monitoring](principal.md#monitoring-and-health) - Metrics, health checks

### Agent Configuration

- [Server Connection](agent.md#server-connection) - Principal address, port
- [Agent Operation](agent.md#agent-operation) - Mode, namespace settings
- [TLS Configuration](agent.md#tls-configuration) - Client TLS settings
- [Logging](agent.md#logging-and-debugging) - Log level, format
- [Monitoring](agent.md#monitoring-and-health) - Metrics, health checks
