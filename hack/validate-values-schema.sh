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

set -euo pipefail

# Script directory
SCRIPT_DIR="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

VALUES_YAML="${REPO_ROOT}/install/helm-repo/argocd-agent-agent/values.yaml"
SCHEMA_JSON="${REPO_ROOT}/install/helm-repo/argocd-agent-agent/values.schema.json"

# Check required binaries
required_binaries="yq jq"
for bin in $required_binaries; do
	if ! command -v "$bin" >/dev/null 2>&1; then
		echo "Error: Required binary '$bin' not found in \$PATH" >&2
		echo "Please install it:" >&2
		if [ "$bin" = "yq" ]; then
			echo "  yq: https://github.com/mikefarah/yq#install" >&2
		elif [ "$bin" = "jq" ]; then
			echo "  jq: https://stedolan.github.io/jq/download/" >&2
		fi
		exit 1
	fi
done

# Check if files exist
if [ ! -f "$VALUES_YAML" ]; then
	echo "Error: values.yaml not found at $VALUES_YAML" >&2
	exit 1
fi

if [ ! -f "$SCHEMA_JSON" ]; then
	echo "Error: values.schema.json not found at $SCHEMA_JSON" >&2
	exit 1
fi

# Function to extract all paths from YAML (recursive)
extract_yaml_paths() {
	local data="$1"
	local prefix="${2:-}"
	
	# Convert YAML to JSON for easier processing
	local json_data
	json_data=$(echo "$data" | yq eval -o=json -)
	
	# Extract all keys recursively
	echo "$json_data" | jq -r '
		def paths_to_strings:
			paths(scalars) as $p | 
			$p | map(tostring) | join(".");
		paths_to_strings
	'
}

# Function to extract all property paths from JSON schema (recursive)
extract_schema_paths() {
	local schema_file="$1"
	
	jq -r '
		def extract_properties($prefix):
			if type == "object" then
				# Handle both schema objects (with .properties) and property objects directly
				(if .properties then .properties else . end) | 
				to_entries[] | 
				($prefix + (if $prefix == "" then "" else "." end) + .key) as $path |
				$path,
				(.value | extract_properties($path))
			else
				empty
			end;
		
		.properties | extract_properties("")
	' "$schema_file"
}

# Function to check if a schema path allows additionalProperties
has_additional_properties() {
	local schema_file="$1"
	local path="$2"
	
	# Check all parent levels of the path
	# Convert path to parts, handling both . and / separators
	local path_parts
	path_parts=$(echo "$path" | sed 's|/|.|g' | tr '.' '\n')
	
	# Check each parent level
	local current_path=""
	for part in $path_parts; do
		if [ -n "$current_path" ]; then
			current_path="${current_path}.${part}"
		else
			current_path="$part"
		fi
		
		# Get the schema for this path level
		local level_schema
		level_schema=$(jq -r --arg path "$current_path" '
			def get_schema($path_parts):
				. as $root |
				reduce $path_parts[] as $part (
					$root.properties // {};
					if type == "object" then
						(if .properties then .properties[$part] else .[$part] end) // {}
					else
						{}
					end
				);
			
			($path | split(".")) as $parts |
			get_schema($parts)
		' "$schema_file")
		
		# Check if this level has additionalProperties
		local additional_props
		additional_props=$(echo "$level_schema" | jq -r '.additionalProperties // false')
		
		if [ "$additional_props" = "true" ]; then
			return 0
		fi
		
		# Check if additionalProperties is an object (which also allows additional props)
		if echo "$additional_props" | jq -e 'type == "object"' >/dev/null 2>&1; then
			return 0
		fi
	done
	
	return 1
}

# Read YAML file
yaml_content=$(cat "$VALUES_YAML")

# Extract paths from YAML
yaml_paths=$(extract_yaml_paths "$yaml_content")

# Extract paths from schema
schema_paths=$(extract_schema_paths "$SCHEMA_JSON")

# Convert to sorted arrays for comparison
yaml_paths_array=$(echo "$yaml_paths" | sort -u)
schema_paths_array=$(echo "$schema_paths" | sort -u)

# Find missing paths, but filter out those allowed by additionalProperties
missing_paths=""
missing_count=0

while IFS= read -r path; do
	[ -z "$path" ] && continue
	
	# Check if path exists in schema
	if echo "$schema_paths_array" | grep -Fxq "$path"; then
		continue
	fi
	
	# Check if path is allowed by additionalProperties
	if has_additional_properties "$SCHEMA_JSON" "$path"; then
		continue
	fi
	
	# This path is missing
	if [ -z "$missing_paths" ]; then
		missing_paths="$path"
	else
		missing_paths="$missing_paths"$'\n'"$path"
	fi
	missing_count=$((missing_count + 1))
done <<< "$yaml_paths_array"

# Report results
if [ "$missing_count" -gt 0 ]; then
	echo "Error: The following fields from values.yaml are missing in values.schema.json:" >&2
	echo "$missing_paths" | while IFS= read -r path; do
		[ -n "$path" ] && echo "  - $path" >&2
	done
	exit 1
else
	echo "✓ All fields from values.yaml are present in values.schema.json"
	exit 0
fi

