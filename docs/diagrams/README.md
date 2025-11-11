# Architecture Diagrams

This directory contains D2 (terrastruct) diagrams documenting the argocd-agent architecture, component boundaries, data flows, and security model.

## Viewing the Diagrams

### Option 1: Using D2 CLI (Recommended)

Install D2:
```bash
# macOS
brew install d2

# Linux
curl -fsSL https://d2lang.com/install.sh | sh -s --
```

Generate SVG diagrams:
```bash
# Generate all diagrams
for f in *.d2; do d2 "$f" "${f%.d2}.svg"; done

# Or generate individual diagrams
d2 01-high-level-architecture.d2 01-high-level-architecture.svg
```

Generate PNG diagrams:
```bash
d2 01-high-level-architecture.d2 01-high-level-architecture.png
```

### Option 2: Using Terrastruct Online

1. Visit [https://play.d2lang.com/](https://play.d2lang.com/)
2. Copy and paste the contents of any `.d2` file
3. View the rendered diagram in your browser
4. Export as SVG or PNG

### Option 3: Using Terrastruct TALA (Professional Layout)

For best results with complex diagrams:
```bash
d2 --layout=tala 01-high-level-architecture.d2 output.svg
```

## Diagram Overview

### [01-high-level-architecture.svg](./01-high-level-architecture.svg)
**Purpose**: High-level overview of the argocd-agent architecture

**Shows**:
- Control plane and workload cluster components
- Component boundaries (principal, agent, Argo CD components)
- Network communication patterns
- Secure (mTLS) vs internal communication
- Optional shared components (Pattern 1 vs Pattern 2)

**Key for**: Understanding the overall system design and deployment options

---

### [02-managed-mode-dataflow.svg](./02-managed-mode-dataflow.svg)
**Purpose**: Detailed data flow for managed mode operation

**Shows**:
- Step-by-step flow from application creation to deployment
- Event flow from control plane to workload cluster (CREATE)
- Status update flow from workload cluster to control plane (STATUS-UPDATE)
- Queue management and event processing
- gRPC stream bidirectionality

**Key for**: Understanding how applications are deployed and managed in managed mode

---

### [03-autonomous-mode-dataflow.svg](./03-autonomous-mode-dataflow.svg)
**Purpose**: Detailed data flow for autonomous mode operation

**Shows**:
- User-driven application creation on workload cluster (kubectl/UI/API)
- Event flow from workload cluster to control plane (mirror)
- Control plane as read-only observer
- Limited operations (sync/refresh) from control plane
- Source of truth differences vs managed mode

**Key for**: Understanding autonomous operation and local application management

---

### [04-security-boundaries.svg](./04-security-boundaries.svg)
**Purpose**: Security architecture and trust zones

**Shows**:
- Trust zone boundaries (control plane, workload clusters, internet)
- Authentication layers (JWT, mTLS, RBAC)
- Secure vs insecure communication channels
- Firewall/network policy boundaries
- Credential management and storage
- Threat mitigation strategies

**Key for**: Security reviews, compliance documentation, understanding attack surface

---

### [05-component-internals.svg](./05-component-internals.svg)
**Purpose**: Internal component architecture and package boundaries

**Shows**:
- Principal internal structure (gRPC layer, APIs, event processing, proxies)
- Agent internal structure (connection, event handling, resource sync)
- Shared internal packages (backend, infrastructure, utilities)
- Kubernetes and Argo CD integration layers
- Package dependencies and boundaries
- Design patterns used

**Key for**: Developers understanding codebase structure and architectural patterns

---

### [06-resync-and-recovery.svg](./06-resync-and-recovery.svg)
**Purpose**: Resync protocol and failure recovery mechanisms

**Shows**:
- Agent restart recovery (managed mode)
- Principal restart recovery (managed and autonomous modes)
- Network partition handling
- Checksum-based synchronization
- Event types for resync protocol
- Recovery guarantees

**Key for**: Understanding reliability, failure handling, and consistency guarantees

---

### [07-resource-proxy.svg](./07-resource-proxy.svg)
**Purpose**: Resource proxy and live resource access mechanism

**Shows**:
- Request flow from Argo CD UI to workload cluster resources
- Resource proxy request tracking and routing
- Redis proxy for cache access
- Cluster shim implementation
- End-to-end flow for viewing live manifests and logs
- Security through mTLS tunnel

**Key for**: Understanding how control plane accesses workload cluster resources without direct network access

## Color Coding

The diagrams use consistent color coding:

- **Green boxes** (`#C8E6C9`): argocd-agent components (principal, agent)
- **Blue boxes** (`#B3E5FC`): Argo CD components (UI, controllers)
- **Yellow/Amber boxes** (`#FFF9C4`): Workload cluster boundaries
- **Light blue backgrounds** (`#E3F2FD`): Control plane cluster boundaries
- **Purple boxes** (`#E1BEE7`): Application workloads
- **Orange boxes** (`#FFE0B2`): External systems (Git, secrets)

### Connection Styles

- **Solid green thick lines**: Secure mTLS communication
- **Dashed blue lines**: Internal in-cluster communication
- **Dashed orange lines**: Optional/pattern-dependent connections
- **Animated lines**: Active data flow
- **Dashed red lines**: Blocked connections

## Customization

You can customize the diagrams by editing the `.d2` files. Key customization points:

1. **Icons**: Change Kubernetes icons by updating the `icon:` properties
2. **Colors**: Modify `style.fill` and `style.stroke` properties
3. **Layout**: Add `direction: down|right|left|up` at the top level
4. **Themes**: Use `--theme` flag when generating (e.g., `--theme=200`)

Example with custom theme:
```bash
d2 --theme=200 --layout=elk 01-high-level-architecture.d2 output.svg
```

## Generating Documentation

To include these diagrams in your documentation:

1. Generate SVG versions (recommended for web):
   ```bash
   for f in *.d2; do d2 --theme=200 "$f" "${f%.d2}.svg"; done
   ```

2. Reference in markdown:
   ```markdown
   ![High Level Architecture](./diagrams/01-high-level-architecture.svg)
   ```

3. For PDF documentation, generate PNG:
   ```bash
   d2 --theme=200 01-high-level-architecture.d2 01-high-level-architecture.png
   ```

## Contributing

When modifying diagrams:

1. **Maintain consistency**: Use the same color scheme and styling
2. **Add tooltips**: Use `tooltip: "description"` for complex elements
3. **Update README**: Document any new diagrams or significant changes
4. **Test rendering**: Verify diagrams render correctly before committing

## Additional Resources

- [D2 Documentation](https://d2lang.com/)
- [D2 Cheat Sheet](https://d2lang.com/tour/cheat-sheet)
- [D2 Playground](https://play.d2lang.com/)
- [Terrastruct TALA Layout Engine](https://terrastruct.com/tala)

## Questions or Issues

For questions about the diagrams or suggestions for improvements, please open an issue in the GitHub repository.
