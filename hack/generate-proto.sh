#!/usr/bin/env bash

set -eo pipefail

PROJECT_ROOT=$(cd "$(dirname "${BASH_SOURCE}")"/..; pwd)
PATH=${PROJECT_ROOT}/build/bin:${PATH}

GENERATE_PATHS="
	${PROJECT_ROOT}/principal/apis/auth;authapi
	${PROJECT_ROOT}/principal/apis/eventstream;eventstreamapi
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
