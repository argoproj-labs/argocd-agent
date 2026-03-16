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
#
# Script for generating release notes using git-cliff
# Generated notes will be from the tag provided and the tag version of before that
# You must be on a release branch for this to work correctly
# For example providing v1.1.0 will get between v1.0.Z, where Z is the latest z stream release
# and 1.1.1 would get between 1.1.1 and 1.1.0
# Must be run in the root of the project
# Usage:
#   ./generate-release-notes.sh [TAG]
#     TAG - tag to generate release notes for (REQUIRED)

TAG="$1"

set -ue

if [[ "$TAG" == "" ]]; then
  echo "Usage: ./generate-release-notes.sh [TAG]"
  exit 1
fi

if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Error: $NEW_TAG is not in the format vX.Y.Z"
  exit 1
fi

Z_VERSION=${TAG#v[0-9]*\.[0-9]*\.}

STARTING_COMMIT_HASH=""
if [[ "$Z_VERSION" == "0" ]]; then
  LAST_RELEASE_BRANCH=$(git branch -a | grep -E 'remotes/origin/release-[0-9]+\.[0-9]+' | tail -n 2 | head -n 1 | xargs)
  STARTING_COMMIT_HASH=$(git merge-base "$LAST_RELEASE_BRANCH" remotes/origin/release-0.7)
else
  PREVIOUS_VERSION_TAG=$(git tag | grep -E "${TAG%\.[0-9]*}" | tail -n 2 | head -n 1)
  STARTING_COMMIT_HASH=$(git rev-parse "${PREVIOUS_VERSION_TAG}"^{})
fi

git-cliff --config cliff.toml -o CHANGELOG.md "$STARTING_COMMIT_HASH".."$TAG"
