#!/bin/bash
# Copyright 2024 The argocd-agent Authors
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

# Generate all D2 diagrams to SVG format
# Usage: ./generate-all.sh [svg|png]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_FORMAT="${1:-svg}"
THEME="${D2_THEME:-0}"
LAYOUT="${D2_LAYOUT:-ELK}"

# Check if d2 is installed
if ! command -v d2 &> /dev/null; then
    echo "Error: d2 is not installed"
    echo "Install with: brew install d2"
    echo "Or visit: https://d2lang.com/"
    exit 1
fi

echo "Generating diagrams with:"
echo "  Format: $OUTPUT_FORMAT"
echo "  Theme: $THEME"
echo "  Layout: $LAYOUT"
echo ""

# Generate all diagrams
for d2_file in "$SCRIPT_DIR"/*.d2; do
    if [ -f "$d2_file" ]; then
        filename=$(basename "$d2_file" .d2)
        output_file="$SCRIPT_DIR/${filename}.${OUTPUT_FORMAT}"

        echo "Generating: ${filename}.${OUTPUT_FORMAT}"
        d2 --theme="$THEME" --layout="$LAYOUT" "$d2_file" "$output_file"
    fi
done

echo ""
echo "✓ All diagrams generated successfully!"
echo "Output directory: $SCRIPT_DIR"
echo ""
echo "To use different settings:"
echo "  D2_THEME=100 D2_LAYOUT=elk ./generate-all.sh png"
