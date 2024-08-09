#!/bin/sh
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

go_packages=$(go list ./...) 
for pkg in $go_packages; do
	path=$(echo $pkg | sed -e 's,github.com/argoproj-labs/argocd-application-agent/,,')
	mkdir -p test/profile/$path
	go test -race -mutexprofile test/profile/$path/mutex.profile $pkg || true
done
