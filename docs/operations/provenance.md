# Verifying Release Artifacts

This page explains argocd-agent's release artifacts and how you can verify them
to prove that they are from the source. Please note, artifacts prior to v0.8.0 cannot
be verified.

### Tools Required

- `cosign` for verifying the signed container image ([install instructions](https://docs.sigstore.dev/cosign/system_config/installation/))

- `slsa-verifier` for verifying the attestations  ([install instructions](https://github.com/slsa-framework/slsa-verifier#installation))

### List of Release Artifacts

| Artifact | Description |
|---|---|
| `argocd-agentctl-darwin-amd64` | CLI binary for Intel Macs |
| `argocd-agentctl-darwin-arm64` | CLI binary for Apple Silicon Macs |
| `argocd-agentctl-linux-amd64` | CLI binary for Linux on Intel CPUs |
| `argocd-agentctl-linux-arm64` | CLI binary for Linux on Arm CPUs |
| `argocd-agentctl_X.Y.Z.intoto.jsonl` | Attestation for CLI binaries |
| `argocd-agentctl_X.Y.Z_checksums.txt` | Checksums for CLI binaries |
| `argocd-agent_X.Y.Z_sbom.tar.gz` | Tar archive containing the SBOM for the CLI & container image |
| `argocd-agent_X.Y.Z_sbom.intoto.jsonl` | Attestation for SBOM tar archive |

## Verifying the Container Image

The container images for the project are signed by cosign using keyless signing.
The following command can be run to verify the image:

```bash
 cosign verify \
--certificate-oidc-issuer https://token.actions.githubusercontent.com \
--certificate-github-workflow-repository "argoproj-labs/argocd-agent" --certificate-identity-regexp https://github.com/argoprojlabs/argocd-agent/.github/workflows/release.yaml@refs/tags/v \
quay.io/argoprojlabs/argocd-agent:vX.Y.Z | jq
```

You should see the output:

```bash
Verification for quay.io/argoprojlabs/argocd-agent:vX.Y.Z --
The following checks were performed on each of these signatures:
  - The cosign claims were validated
  - Existence of the claims in the transparency log was verified offline
  - The code-signing certificate was verified using trusted certificate authority certificates
[
  {
    "critical": {
      "identity": {
        "docker-reference": "quay.io/argoprojlabs/argocd-agent:vX.Y.Z"
      },
      "image": {
        "docker-manifest-digest": "some digest"
      },
      "type": "https://sigstore.dev/cosign/sign/v1"
    },
    "optional": {
      "repo": "argoproj-labs/argocd-agent",
      "sha": "some sha",
      "workflow": "Publish argocd-agent release"
    }
  }
]
```

A [SLSA](https://slsa.dev/) level 3 provenance is also generated for the image using the
[slsa-github-generator](https://github.com/slsa-framework/slsa-github-generator).
The following command can be used to verify the image's attestation.

```bash
# You can also use an image's digest to verify
slsa-verifier verify-image quay.io/argoprojlabs/argocd-agent:vX.Y.Z \
    --source-uri github.com/argoproj-labs/argocd-agent \
    --source-tag vX.Y.Z
```

You should see the output:

```bash
Verified build using builder "https://github.com/slsa-framework/slsa-github-generator/.github/workflows/generator_container_slsa3.yml@refs/tags/v2.1.0" at commit [some commit sha]
PASSED: SLSA verification passed
```

## Verifying the CLI Binaries

An attestation is generated for the CLI binaries for each release. You can use slsa-verifier to check if the binary has been compiled on our GitHub workflows by running the following command:

```bash
slsa-verifier verify-artifact argocd-agentctl-[linux/darwin]-[amd64/arm64] \
  --provenance-path argocd-agentctl_X.Y.Z.intoto.jsonl \
  --source-uri github.com/argoproj-labs/argocd-agent \
  --source-tag vX.Y.Z
```

The following output should be shown:

```bash
Verified build using builder "https://github.com/slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@refs/tags/v2.1.0" at commit [some commit sha]
Verifying artifact argocd-agentctl-[linux/darwin]-[amd64/arm64]: PASSED

PASSED: SLSA verification passed
```

## Verifying the SBOM

A SBOM is generated for each release. There are two SBOMs that are packaged into a tar archive.
One is for the container image and the other is for the go dependencies. It also has an
attestation provided to verify the archive. This can be verified by running the following command:

```bash
slsa-verifier verify-artifact argocd-agent_X.Y.Z_sbom.tar.gz \
  --provenance-path argocd-agent_X.Y.Z_sbom.intoto.jsonl \
  --source-uri github.com/argoproj-labs/argocd-agent \
  --source-tag vX.Y.Z
```

You should see the following output:

```
Verified build using builder "https://github.com/slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@refs/tags/v2.1.0" at commit [some commit sha]
Verifying artifact argocd-agent_X.Y.Z_sbom.tar.gz: PASSED

PASSED: SLSA verification passed
```
