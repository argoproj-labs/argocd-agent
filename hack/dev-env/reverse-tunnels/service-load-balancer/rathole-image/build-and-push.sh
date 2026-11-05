#!/bin/bash

# Build and push a multi-arch docker container containing latest rathole release built from source.

# I needed to run this first:
# - docker buildx create --name mybuilder --driver docker-container --use
# I'm on Linux x64 (Fedora)

REPO=quay.io/jgwest-redhat

set -ex

SCRIPTPATH="$(
    cd -- "$(dirname "$0")" >/dev/null 2>&1 || exit
    pwd -P
)"

TEMP_DIR=$(mktemp -d)

cd $TEMP_DIR
git clone https://github.com/yujqiao/rathole

cd rathole

git checkout ebb764ae53d7ffe4fcb45f83f7563bec5c74199d  # v0.5.0

cp $SCRIPTPATH/artifacts/Dockerfile.new $TEMP_DIR/rathole/Dockerfile

docker buildx build -t $REPO/rathole:latest --platform linux/amd64,linux/arm64 --push .

