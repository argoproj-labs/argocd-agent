#!/bin/bash
# Copyright 2025 The argocd-agent Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -e -o pipefail

# TARGET_REMOTE is the Git remote configured to point to the upstream
TARGET_REMOTE=${TARGET_REMOTE:-upstream}
# TARGET_VERSION is the version to release. Must be in format X.Y.Z
TARGET_VERSION=$1

if test "${TARGET_VERSION}" = ""; then
	echo "Usage: $0 <version>" >&2
	echo
	echo "Version must be in format X.Y.Z without any prefix or suffix"
	exit 1
fi

if ! echo "${TARGET_VERSION}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
        echo "Error: Target version '${TARGET_VERSION}' is not well-formed. Must be X.Y.Z (without v prefix)" >&2
        exit 1
fi

TARGET_TAG="v${TARGET_VERSION}"

RELEASE_BRANCH=$(git rev-parse --abbrev-ref HEAD || true)
if [[ ${RELEASE_BRANCH} = release-* ]]; then
        echo "***   branch is ${RELEASE_BRANCH}"
        export IMAGE_TAG=${TARGET_TAG}
else
        echo "Error: Current branch '${RELEASE_BRANCH}' seems not to be a release branch" >&2
        exit 1
fi

if ! test -f VERSION; then
        echo "Error: You should be in repository root." >&2
        exit 1
fi

echo "${TARGET_VERSION}" > VERSION

echo "*** checking for existence of git tag ${TARGET_TAG}"
if git tag -l "${TARGET_TAG}" | grep -q "${TARGET_TAG}"; then
        echo "Error: Tag with version ${TARGET_TAG} already exists." >&2
        exit 1
fi

echo "*** generating new manifests"
(
	cd install/kubernetes/agent
	kustomize edit set image ghcr.io/argoproj-labs/argocd-agent/argocd-agent-agent=quay.io/argoprojlabs/argocd-agent-agent:${TARGET_TAG}
)
(
	cd install/kubernetes/principal
	kustomize edit set image ghcr.io/argoproj-labs/argocd-agent/argocd-agent-principal=quay.io/argoprojlabs/argocd-agent-principal:${TARGET_TAG}
)

echo "*** committing changes to release branch"

git add VERSION install/kubernetes/{agent,principal}/kustomization.yaml
git commit -S -s -m "release: Publish release ${TARGET_VERSION}"

echo "*** building release images"

for component in agent principal; do
	export IMAGE_REPOSITORY="quay.io/argoprojlabs"
	make image-${component}
done

echo "*** creating release tag"

git tag ${TARGET_TAG}

echo "*** Done."
echo
echo "To finalize release: "
echo
echo "1) Push images:"
echo
echo "podman push ${IMAGE_REPOSITORY}/argocd-agent-agent:${TARGET_TAG}"
echo "podman push ${IMAGE_REPOSITORY}/argocd-agent-principal:${TARGET_TAG}"
echo
echo "2) Push Git changes and tag"
echo
echo "git push ${TARGET_REMOTE} ${RELEASE_BRANCH}"
echo "git push ${TARGET_REMOTE} ${TARGET_TAG}"
echo
echo "3) Create the release on GitHub"