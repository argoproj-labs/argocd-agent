#!/bin/bash
# Copyright 2026 The argocd-agent Authors
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

# This wrapper script runs a Go command with data race detection enabled
# and monitors for data races. When a data race is detected, it kills
# the process.

set -e

# Check if ENABLE_DATA_RACE_DETECTOR is set (defaults to true for e2e tests)
ENABLE_DATA_RACE_DETECTOR=${ENABLE_DATA_RACE_DETECTOR:-true}

if [ "$ENABLE_DATA_RACE_DETECTOR" != "true" ]; then
    # Race detection disabled, run command normally
    exec "$@"
    exit $?
fi

# Create a temporary file to store output
DATA_RACE_LOG=$(mktemp)
trap "rm -f $DATA_RACE_LOG" EXIT

# Create a named pipe for monitoring
PIPE=$(mktemp -u)
mkfifo "$PIPE"
trap "rm -f $PIPE $DATA_RACE_LOG" EXIT

# Background process to monitor for race conditions
(
    while IFS= read -r line; do
        echo "$line"
        echo "$line" >> "$DATA_RACE_LOG"

        # Check if we've detected a data race
        if echo "$line" | grep -q "WARNING: DATA RACE"; then
            echo "========================================" >&2
            echo "DATA RACE DETECTED - TERMINATING PROCESS" >&2
            echo "========================================" >&2
            # Kill the parent process group
            kill -TERM -$$ 2>/dev/null || true
            exit 1
        fi
    done < "$PIPE"
) &
MONITOR_PID=$!

# Extract the go command and arguments
# If first arg is "go", inject -race flag after "run"
if [ "$1" = "go" ]; then
    shift
    if [ "$1" = "run" ]; then
        shift
        # Run with race detection enabled
        go run -race "$@" 2>&1 | tee "$PIPE"
        RESULT=${PIPESTATUS[0]}
    else
        # Not a "go run" command, just pass through
        go "$@" 2>&1 | tee "$PIPE"
        RESULT=${PIPESTATUS[0]}
    fi
else
    # Not a go command, just run it
    "$@" 2>&1 | tee "$PIPE"
    RESULT=${PIPESTATUS[0]}
fi

# Clean up
kill $MONITOR_PID 2>/dev/null || true
wait $MONITOR_PID 2>/dev/null || true

exit $RESULT
