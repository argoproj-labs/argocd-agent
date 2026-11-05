#!/bin/bash

SCRIPTPATH="$(
  cd -- "$(dirname "$0")" >/dev/null 2>&1 || exit
  pwd -P
)"

DOCKER_BIN=${DOCKER_BIN:-"docker"}

"$DOCKER_BIN" run --network host --name frp-proxy -it --rm -v "$SCRIPTPATH/frpc.toml:/app/config.toml":Z -v "$SCRIPTPATH/frp-ca.crt:/app/frp-ca.crt":Z "docker.io/fatedier/frpc:v0.71.0@sha256:99ece6a2b62cfc68731e0df289af804ff1c699911cfc47871856434f1d6d53ee" -c /app/config.toml

# or, without docker:
#
# frpc -c $SCRIPTPATH/config.toml
