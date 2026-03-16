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

# Script for triggering the release workflow on GitHub by pushing a new tag to the branch
# This script is intended to run in the root of the project
# Usage:
#   ./trigger-release.sh [NEW_TAG] [GIT_REMOTE] [BRANCH]
#     NEW_TAG - must be in the format of v.X.Y.Z and X.Y must match with the release branch (REQUIRED)
#     GIT_REMOTE - the git remote to push the tag to (REQUIRED)
#     BRANCH - optional argument to specify the branch to push to, if specified will switch to that branch
#              then switch back to the original branch after pushing

NEW_TAG="$1"
GIT_REMOTE="$2"
BRANCH="$3"

set -ue

if [[ "$NEW_TAG" == "" ]] || [[ "$GIT_REMOTE" = "" ]]; then
  echo "Usage: ./trigger-release.sh [NEW_TAG] [GIT_REMOTE]"
  exit 1
fi

if [[ ! "$NEW_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Error: $NEW_TAG is not in the format vX.Y.Z"
  exit 1
fi

ORIGINAL_BRANCH=""
if [[ "$BRANCH" != "" ]]; then
  ORIGINAL_BRANCH=$(git branch --show-current)
  git checkout $BRANCH
fi

CURRENT_BRANCH=$(git branch --show-current)
RELEASE_BRANCH="release-${NEW_TAG#v}"
RELEASE_BRANCH="${RELEASE_BRANCH%\.[0-9]*}"

if [[ "$CURRENT_BRANCH" != "$RELEASE_BRANCH" ]]; then
  echo "Error: Current branch is not $RELEASE_BRANCH"
  exit 1
fi

echo "> Starting release of argocd-agent $NEW_TAG"

REMOTE_URL=$(git remote get-url "$GIT_REMOTE")
echo "$REMOTE_URL"
if [[ "$REMOTE_URL" =~ argoproj-labs/argocd-agent ]]; then
  echo "!! WARNING: You are about to make an official argocd-agent release!"
  echo "   This will create the container image, CLI binaries, and a GitHub release."
  echo "   The release this will create will be visable to everyone."
  echo "   If your intention is to create a release on a fork please change your remote to point to that."
  echo ""
  echo "   Remote URL: $REMOTE_URL"
  echo "   Version To Be Released: $NEW_TAG"
  echo ""
  echo "> Please type 'y' if you would like to continue... "
  read -r confirmation
  if [[ "$confirmation" != "y" ]]; then
    echo "Cancelled."
  fi
fi

echo "> Making sure branch is up to date"
git pull "$GIT_REMOTE" "$CURRENT_BRANCH"

echo "> Verifying that init release workflow has been run"

VERSION_CONTENTS=$(cat VERSION)
if [[ "$VERSION_CONTENTS" != "${NEW_TAG#v}" ]]; then
  echo "Error: VERSION file is $VERSION_CONTENTS and not ${NEW_TAG#v}, was the init release workflow run?"
  exit 1
fi

PRINCIPAL_KUSTOMIZATION=$(cat install/kubernetes/principal/kustomization.yaml)
if [[ ! "${PRINCIPAL_KUSTOMIZATION}" =~ "${NEW_TAG#v}" ]]; then
  echo "Error: principal kustomization does not have the new tag, was the init release workflow run?"
  exit 1
fi

AGENT_KUSTOMIZATION=$(cat install/kubernetes/agent/kustomization.yaml)
if [[ ! "${AGENT_KUSTOMIZATION}" =~ "${NEW_TAG#v}" ]]; then
  echo "Error: agent kustomization does not have the new tag, was the init release workflow run?"
  exit 1
fi

echo "> Verifying that tag does not exist on remote"
if [[ "$(git ls-remote --tags origin "$NEW_TAG")" =~ "${NEW_TAG}$" ]]; then
  echo "Error: $NEW_TAG exists on the remote"
  exit 1
fi

echo "> Creating and pushing new tag $NEW_TAG"
git tag "$NEW_TAG"
git push "$GIT_REMOTE" "$NEW_TAG"

echo "> Tag created and pushed! Release workflow should be running."
