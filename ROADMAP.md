# Roadmap

This roadmap outlines the main areas we're working on for argocd-agent. Items are organized by theme rather than timeline - we're building incrementally and priorities may shift based on user feedback and technical discoveries.

**Important Notes:**
- This is a living document that changes as we learn more
- Not all items are planned for v1.0 - we're focused on getting the basics right first
- Community contributions are welcome for any area

See our [GitHub milestones](https://github.com/argoproj-labs/argocd-agent/milestones) and [issues](https://github.com/argoproj-labs/argocd-agent/issues) for detailed progress tracking.

## Testing and Documentation

Making the project more reliable and easier to use.

- **Better end-to-end tests**: Multi-cluster scenarios and failure modes
- **Improved documentation**: User guides, troubleshooting, and operational docs  
- **Performance testing**: Understanding scaling limits and characteristics
- **Bug fixes and stability**: Address edge cases and improve error handling

## Security

Improving authentication and access control.

- **[SPIFFE/SPIRE support](https://github.com/argoproj-labs/argocd-agent/issues/345)**: Better identity verification
- **[Private repository support](https://github.com/argoproj-labs/argocd-agent/issues/474)**: Sync Git credentials to agents
- **Better RBAC**: Multi-tenant access controls

## Usability

Making day-to-day operations easier.

- **Better CLI tools**: Easier agent bootstrapping and registration
- **ApplicationSet support**: Full lifecycle management in distributed setups
- **[Observability improvements](https://github.com/argoproj-labs/argocd-agent/issues/119)**: OpenTelemetry integration and better metrics
- **[Pod log streaming](https://github.com/argoproj-labs/argocd-agent/issues/128)**: Access logs from remote clusters
- **[Terminal access](https://github.com/argoproj-labs/argocd-agent/issues/129)**: Debug pods on remote clusters

## Scalability

Supporting larger deployments.

- **[Principal high availability](https://github.com/argoproj-labs/argocd-agent/issues/186)**: Multi-instance deployments
- **Alternative storage backends**: Database options for larger scale
- **[Protocol optimization](https://github.com/argoproj-labs/argocd-agent/issues/113)**: Data compression for slow networks
- **Better scaling**: Horizontal scaling of principal components

## Community Input

We want to hear from users about what matters most. The best ways to influence the roadmap:

- **[GitHub Discussions](https://github.com/argoproj-labs/argocd-agent/discussions)** - Share your use cases
- **Code contributions** - Implement what you need
- **Testing and feedback** - Try it out and let us know what breaks
- **Documentation** - Help make things clearer

Real-world experience helps us prioritize what to work on next.
