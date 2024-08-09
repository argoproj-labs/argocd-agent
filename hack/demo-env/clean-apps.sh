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

# Script to clean all apps in the demo environment. It will remove any resource
# finalizer before deletion, so it might leave your workloads behind.
for cluster in control-plane agent-managed agent-autonomous; do
	apps=$(kubectl --context vcluster-${cluster} get apps -A --no-headers -o go-template="{{range .items}}{{.metadata.namespace}} {{.metadata.name}}{{end}}")
	test -z "$apps" && continue
	OIFS="$IFS"
	while IFS= read -r app; do
		IFS=" " set -- $app
		namespace="$1"
		name="$2"
		echo "Patching $namespace/$name in vcluster-${cluster}"
		kubectl --context vcluster-${cluster} patch -n $namespace app $name -p '{"metadata":{"finalizers":null}}' --type=merge
	done < <(echo "$apps")
	echo "Deleting all apps in vcluster-${cluster}"
	kubectl --context vcluster-${cluster} delete apps --all-namespaces --all
done
