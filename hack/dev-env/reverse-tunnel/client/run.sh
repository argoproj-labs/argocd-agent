#!/bin/bash

SCRIPTPATH="$(
  cd -- "$(dirname "$0")" >/dev/null 2>&1 || exit
  pwd -P
)"

docker run --network host  --name rathole-proxy -it --rm -v "$SCRIPTPATH/client.toml:/app/config.toml" "quay.io/jgwest-redhat/rathole:latest@sha256:53999f80b69f9a5020e19e9c9be90fc34b973d9bd822d4fd44b968f2ebe0845f" --client /app/config.toml
# Container image is built from 'rathole-image' directory

# or, without docker:
# 
# rathole --client $SCRIPTPATH/config.toml

