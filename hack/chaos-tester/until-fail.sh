#!/bin/bash

while "$@"; do :; done

# Use this script to run a command over and over until it fails.
#
# Example:
# > Run E2E tests over and over until they fail.
# until-fail.sh make test-e2e