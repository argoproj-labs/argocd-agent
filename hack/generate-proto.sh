#!/usr/bin/env bash
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


set -eo pipefail

PROJECT_ROOT=$(cd "$(dirname "${BASH_SOURCE}")"/..; pwd)
PATH=${PROJECT_ROOT}/build/bin:${PATH}

GENERATE_PATHS="
	${PROJECT_ROOT}/principal/apis/auth;authapi
	${PROJECT_ROOT}/principal/apis/eventstream;eventstreamapi
	${PROJECT_ROOT}/principal/apis/resource;resourceapi
	${PROJECT_ROOT}/principal/apis/version;versionapi
"

for p in ${GENERATE_PATHS}; do
	set -x
	IFS=";"
	set -- $p
	src_path=$1
	api_name=$2
	unset IFS
	files=
	for f in $(ls $src_path/*.proto); do
		echo "--> Generating Protobuf and gRPC client for $api_name"
		mkdir -p ${PROJECT_ROOT}/pkg/api/grpc/${api_name}
		${PROJECT_ROOT}/build/bin/protoc  -I=${src_path} \
			-I=${PROJECT_ROOT}/vendor \
			-I=${PROJECT_ROOT}/proto \
			-I=${PROJECT_ROOT}/build/bin/protoc-include \
			--go_out=${PROJECT_ROOT}/pkg/api/grpc/${api_name} \
			--go_opt=paths=source_relative \
			--go-grpc_out=${PROJECT_ROOT}/pkg/api/grpc/${api_name} \
			--go-grpc_opt=paths=source_relative \
			$f
	done
done
