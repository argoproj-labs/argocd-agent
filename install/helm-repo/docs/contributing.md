# Helm Chart Contribution Guidelines

This document provides guidelines for contributing to the ArgoCD Agent Helm chart located in `install/helm-repo/argocd-agent-agent/`.

## Overview

The Helm chart structure follows standard Helm conventions:

```
argocd-agent-agent/
├── Chart.yaml          # Chart metadata and version
├── values.yaml         # Default configuration values
├── values.schema.json  # JSON schema for values validation
├── README.md           # Auto-generated documentation
└── templates/          # Kubernetes resource templates
    ├── _helpers.tpl    # Template helpers
    ├── *.yaml          # Resource templates
    └── tests/          # Helm test templates
```

## Prerequisites

Before making changes, ensure you have the following tools installed:

- **Helm CLI** (v3.8.0 or newer): Required for chart validation and testing
- **yq**: Required for values schema validation
- **jq**: Required for values schema validation
- **helm-docs**: Required for regenerating README.md (installed via `make install-helm-docs`)

## Making Changes

### 1. Updating `values.yaml`

When adding or modifying configuration values:

1. **Add the new value** to `values.yaml` with appropriate defaults and comments
2. **Update `values.schema.json`** to include the new field (see section below)
3. **Update templates** if the new value needs to be used in Kubernetes resources
4. **Regenerate README.md** using `make generate-helm-docs`
5. **Validate** your changes using `make validate-values-schema`

Example:

```yaml
# values.yaml
newFeature:
  enabled: false
  timeout: "30s"
```

### 2. Updating `values.schema.json`

The JSON schema validates Helm values and provides IDE autocomplete. **Every field in `values.yaml` must have a corresponding entry in `values.schema.json`**.

#### Adding a New Field

1. Locate the appropriate parent object in the schema
2. Add the new property with appropriate type and description

Example for the `newFeature` value above:

```json
{
  "properties": {
    "newFeature": {
      "type": "object",
      "description": "Configuration for the new feature",
      "properties": {
        "enabled": {
          "type": "boolean",
          "description": "Enable the new feature",
          "default": false
        },
        "timeout": {
          "type": "string",
          "description": "Timeout duration for the feature",
          "default": "30s"
        }
      }
    }
  }
}
```

#### Schema Types

- `string`: Text values
- `integer`: Numbers
- `boolean`: true/false
- `object`: Nested objects
- `array`: Lists of items

#### Validation

After updating the schema, run:

```bash
make validate-values-schema
```

This ensures all fields in `values.yaml` are present in `values.schema.json`.

### 3. Updating Templates

When modifying Kubernetes resource templates in `templates/`:

1. **Follow Helm best practices**: Use template helpers from `_helpers.tpl` for common patterns
2. **Use proper indentation**: Templates should render valid YAML
3. **Add conditional logic**: Use `{{- if .Values.feature.enabled }}` for optional features
4. **Test locally**: Use `helm template` to verify template rendering

Example:

```yaml
{{- if .Values.newFeature.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "argocd-agent-agent.fullname" . }}-new-feature
data:
  timeout: {{ .Values.newFeature.timeout | quote }}
{{- end }}
```

#### Testing Templates

Render templates locally to verify they work:

```bash
# Render all templates with default values
helm template argocd-agent ./install/helm-repo/argocd-agent-agent

# Render with custom values
helm template argocd-agent ./install/helm-repo/argocd-agent-agent \
  -f my-values.yaml

# Dry-run installation
helm install argocd-agent ./install/helm-repo/argocd-agent-agent \
  --dry-run --debug
```

### 4. Regenerating README.md

The `README.md` file is auto-generated from chart metadata and values documentation. After modifying `values.yaml` or `Chart.yaml`, regenerate it:

```bash
make generate-helm-docs
```

This uses `helm-docs` (v1.13.1) to generate the documentation. The generated README includes:
- Chart metadata (version, type, app version)
- Complete values table with descriptions
- Maintainer information

**Important**: Always commit the regenerated `README.md` with your changes.

## Validation Checklist

Before submitting a pull request, ensure:

- [ ] All new `values.yaml` fields are documented in `values.schema.json`
- [ ] `make validate-values-schema` passes without errors
- [ ] `make generate-helm-docs` has been run and `README.md` is updated
- [ ] Templates render correctly with `helm template`
- [ ] Chart version is incremented if needed
- [ ] No syntax errors in YAML/JSON files

### Running Validations

```bash
# Validate values schema
make validate-values-schema

# Generate helm-docs (updates README.md)
make generate-helm-docs

# Verify chart is valid
helm lint ./install/helm-repo/argocd-agent-agent

# Test template rendering
helm template argocd-agent ./install/helm-repo/argocd-agent-agent
```

## Testing

### Helm Chart Tests

The chart includes test templates in `templates/tests/`. To run tests:

1. **Enable tests** in `values.yaml`:
   ```yaml
   tests:
     enabled: true
   ```

2. **Install the chart**:
   ```bash
   helm install argocd-agent ./install/helm-repo/argocd-agent-agent
   ```

3. **Run tests**:
   ```bash
   helm test argocd-agent
   ```

Available test templates:
- `test-configMap.yaml`: Validates ConfigMap creation
- `test-deployment.yaml`: Validates Deployment creation
- `test-labels.yaml`: Validates label consistency
- `test-overall.yaml`: Validates overall chart installation
- `test-rbac.yaml`: Validates RBAC resources
- `test-sa.yaml`: Validates ServiceAccount creation
- `test-services.yaml`: Validates Service creation

## CI/CD Expectations

The CI pipeline automatically validates Helm chart changes:

1. **Schema Validation**: Ensures `values.yaml` and `values.schema.json` are synchronized
2. **Helm-Docs Validation**: Ensures `README.md` matches the current chart state
3. **Chart Linting**: Validates chart structure and best practices

**Important**: If CI fails, fix the issues before requesting review. Common failures:
- Missing fields in `values.schema.json`
- Outdated `README.md` (run `make generate-helm-docs`)
- Chart linting errors

**Note**: This document is specific to Helm chart contributions. For general project contribution guidelines, see the [main contributing guide](../../../docs/CONTRIBUTING.md).
