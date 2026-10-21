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

set -e
set -o pipefail

# enable for debugging:
# set -x

# ARGOCD_VERSION specifies the version of Argo CD to install.
# Can be one of stable, latest or a specific version (e.g. v3.2.2)
ARGOCD_VERSION=${ARGOCD_VERSION:-stable}

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
source ${SCRIPTPATH}/namespaces.sh
VCLUSTERS="control-plane:${ARGOCD_PRINCIPAL_NAMESPACE} agent-managed:${ARGOCD_MANAGED_NAMESPACE} agent-autonomous:${ARGOCD_AUTONOMOUS_NAMESPACE}"
VCLUSTERS_AGENTS="agent-managed:${ARGOCD_MANAGED_NAMESPACE} agent-autonomous:${ARGOCD_AUTONOMOUS_NAMESPACE}"
gen_admin_pwd="${ARGOCD_AGENT_GEN_ADMIN_PWD:-true}"
action="$1"
shift

LB_NETWORK=${LB_NETWORK:-192.168.56}

required_binaries="kubectl jq htpasswd kustomize vcluster git"
for bin in $required_binaries; do
	which $bin >/dev/null 2>&1 || (echo "Required binary $bin not found in \$PATH" >&2; exit 1)
done

# Kubectl context to restore
initial_context=$(kubectl config current-context)

cleanup() {
    kubectl config use-context ${initial_context}
}

on_error() {
    echo "ERROR: Error occurred, terminating." >&2
    cleanup
}

cluster() {
    IFS=":" s=($1); echo ${s[0]}
}

namespace() {
    IFS=":" s=($1); echo ${s[1]}
}

trap cleanup EXIT
trap on_error ERR

# check_for_openshift looks for cluster OpenShift API Resources, and if found, sets OPENSHIFT=true
check_for_openshift() {

    OPENSHIFT=
    if (kubectl api-resources || true) | grep -q "openshift.io"; then
        OPENSHIFT=true
    fi

}

# Optional: expose_vcluster_via_route creates a passthrough OpenShift Route for the (not yet created)
# vcluster Service, and prints the path to a generated Helm values file that configures the
# vcluster to be signed for, and reachable at, that Route's host. This is used instead of
# 'vcluster create --expose' (which provisions a cloud LoadBalancer Service) since LoadBalancers
# can be expensive on public cloud, or unavailable entirely.
# OpenShift assigns a Route a host as soon as it's created, regardless of whether the Service it
# targets exists yet, which is what allows the vcluster's proxy certificate to be signed for that
# host (controlPlane.proxy.extraSANs), and its exported kubeconfig to point at it
# (exportKubeConfig.server), from values passed to the 'vcluster create' call that follows.
#
# Arguments: vcluster name (also used as its namespace, by convention of this script).
expose_vcluster_via_route() {
    local name="$1"

    cat <<EOF | kubectl --context=${initial_context} -n ${name} apply -f - >&2
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${name}
spec:
  to:
    kind: Service
    name: ${name}
  port:
    targetPort: https
  tls:
    termination: passthrough
EOF

    local route_host=
    for ((i = 1; i <= 60; i++)); do
        route_host=$(kubectl --context=${initial_context} -n ${name} get route ${name} -o jsonpath='{.spec.host}') || true
        if [ -n "$route_host" ]; then
            break
        fi
        sleep 2
    done
    if [ -z "$route_host" ]; then
        echo "ERROR: Failed to obtain Route host for ${name}" >&2
        exit 1
    fi
    echo "Route host for ${name} is ${route_host}" >&2

    local values_file=$(mktemp)
    cat <<EOF > ${values_file}
exportKubeConfig:
  server: https://${route_host}:443
controlPlane:
  proxy:
    extraSANs:
    - ${route_host}
EOF
    echo ${values_file}
}

# Optional: wait_for_vcluster_kubeconfig_for_route polls for the Secret vcluster generates with its own, pre-signed
# kubeconfig (named 'vc-<name>', available as soon as the vcluster control plane is up - well
# before it would respond successfully through the Route), and merges it into the local
# kubeconfig under the given context name. 
# 
# This allow the vcluster K8s API to be accessed (via exposure via Route) after this setup script has completed.
#
# - This replaces the kubeconfig setup normally done by 'vcluster create --connect', which (lacking a LoadBalancer address to use) would otherwise try to reach the vcluster via local port-forwarding. But this doesn't give us the stable, directly reachable context the rest of this dev-env's scripts expect.
#
# Arguments: vcluster name (also used as its namespace), context name to write locally.
wait_for_vcluster_kubeconfig_for_route() {
    local name="$1"
    local context_name="$2"

    echo "-> Waiting for vcluster $name kubeconfig Secret"
    local tmp_kubeconfig=$(mktemp)
    for ((i = 1; i <= 60; i++)); do
        kubectl --context=${initial_context} -n ${name} get secret vc-${name} -o jsonpath='{.data.config}' 2>/dev/null | base64 -d > ${tmp_kubeconfig} || true
        if [ -s "$tmp_kubeconfig" ]; then
            break
        fi
        sleep 5
    done
    if [ ! -s "$tmp_kubeconfig" ]; then
        echo "ERROR: Failed to obtain kubeconfig Secret vc-${name}" >&2
        exit 1
    fi

    local server=$(kubectl --kubeconfig=${tmp_kubeconfig} config view --minify --raw -o jsonpath='{.clusters[0].cluster.server}')
    local cert_dir=$(mktemp -d)
    kubectl --kubeconfig=${tmp_kubeconfig} config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d > ${cert_dir}/ca.crt
    kubectl --kubeconfig=${tmp_kubeconfig} config view --minify --raw -o jsonpath='{.users[0].user.client-certificate-data}' | base64 -d > ${cert_dir}/client.crt
    kubectl --kubeconfig=${tmp_kubeconfig} config view --minify --raw -o jsonpath='{.users[0].user.client-key-data}' | base64 -d > ${cert_dir}/client.key
    rm -f ${tmp_kubeconfig}

    kubectl config set-cluster ${context_name} --server="${server}" --certificate-authority="${cert_dir}/ca.crt" --embed-certs=true
    kubectl config set-credentials ${context_name} --client-certificate="${cert_dir}/client.crt" --client-key="${cert_dir}/client.key" --embed-certs=true
    kubectl config set-context ${context_name} --cluster=${context_name} --user=${context_name}
    rm -rf ${cert_dir}

    echo "-> Waiting for vcluster $name API to become reachable via Route"
    for ((i = 1; i <= 60; i++)); do
        if kubectl --context=${context_name} get --raw /healthz >/dev/null 2>&1; then
            return 0
        fi
        sleep 5
    done
    echo "ERROR: vcluster $name API not reachable via Route after waiting" >&2
    exit 1
}


# wait_for_pods waits for all Pods in the given context and namespace to be running.
# Arguments: context, component, namespace
wait_for_pods() {
    context="$1"
    component="$2"
    ns="$3"

    case "$component" in
    "principal")
        kubectl --context $context -n $ns rollout status --watch deployments argocd-server
        kubectl --context $context -n $ns rollout status --watch deployments argocd-repo-server
        kubectl --context $context -n $ns rollout status --watch deployments argocd-dex-server
        kubectl --context $context -n $ns rollout status --watch deployments argocd-redis
        ;;
    "agent")
        kubectl --context $context -n $ns rollout status --watch statefulsets argocd-application-controller
        kubectl --context $context -n $ns rollout status --watch deployments argocd-repo-server
        kubectl --context $context -n $ns rollout status --watch deployments argocd-redis
        ;;
     *)
        echo "Unknown component: $component"
        exit 1
        ;;
     esac
}


apply() {

    TMP_DIR=`mktemp -d`

    echo "-> TMP_DIR is $TMP_DIR"
    cp -r ${SCRIPTPATH}/* $TMP_DIR

    mkdir -p ${TMP_DIR}/argo-cd
    cd ${TMP_DIR}/argo-cd
    git init
    git remote add origin https://github.com/argoproj/argo-cd
    if [ "$ARGOCD_VERSION" = "latest" ]; then
        fetch_branch=master
    else
        fetch_branch=${ARGOCD_VERSION}
    fi
    git fetch --depth=1 origin ${fetch_branch}
    git checkout FETCH_HEAD
    cd -

    # Comment out 'loadBalancerIP:' lines on OpenShift
    if [[ "$OPENSHIFT" != "" ]]; then
        sed -i.bak -e '/loadBalancerIP/s/^/#/' $TMP_DIR/control-plane/redis-service.yaml
        sed -i.bak -e '/loadBalancerIP/s/^/#/' $TMP_DIR/control-plane/server-service.yaml
    else
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/control-plane/redis-service.yaml
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/control-plane/server-service.yaml
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/agent-managed/redis-service.yaml
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/agent-autonomous/redis-service.yaml
    fi

    # Get the most recent version of Argo CD from the releases page
    if [ "$ARGOCD_VERSION" = "stable" ]; then
        LATEST_RELEASE_TAG=`curl -s "https://api.github.com/repos/argoproj/argo-cd/releases" | jq -r '.[].tag_name' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1`
    else
        LATEST_RELEASE_TAG=${ARGOCD_VERSION}
    fi
    if [ -z "${LATEST_RELEASE_TAG}" ]; then
        echo "ERROR: Could not determine Argo CD release tag. Check network/API access." >&2
        exit 1
    fi
    sed -i.bak -e "s/LatestReleaseTag/${LATEST_RELEASE_TAG}/" $TMP_DIR/control-plane/kustomization.yaml
    sed -i.bak -e "s/LatestReleaseTag/${LATEST_RELEASE_TAG}/" $TMP_DIR/agent-autonomous/kustomization.yaml
    sed -i.bak -e "s/LatestReleaseTag/${LATEST_RELEASE_TAG}/" $TMP_DIR/agent-managed/kustomization.yaml
    (cd $TMP_DIR/control-plane && kustomize edit set namespace ${ARGOCD_PRINCIPAL_NAMESPACE})
    (cd $TMP_DIR/agent-managed && kustomize edit set namespace ${ARGOCD_MANAGED_NAMESPACE})
    (cd $TMP_DIR/agent-autonomous && kustomize edit set namespace ${ARGOCD_AUTONOMOUS_NAMESPACE})
    sed -i.bak -e "s/PrincipalNamespacePlaceholder/${ARGOCD_PRINCIPAL_NAMESPACE}/" $TMP_DIR/control-plane/namespace-transformer.yaml
    sed -i.bak -e "s/ManagedNamespacePlaceholder/${ARGOCD_MANAGED_NAMESPACE}/" $TMP_DIR/agent-managed/namespace-transformer.yaml
    sed -i.bak -e "s/AutonomousNamespacePlaceholder/${ARGOCD_AUTONOMOUS_NAMESPACE}/" $TMP_DIR/agent-autonomous/namespace-transformer.yaml

    # Generate the server secret key for the argocd running on the managed and autonomous agent clusters
    echo "-> Generate server.secretkey for agent's argocd-secrets"
    if ! pwmake=$(which pwmake); then
        pwmake=$(which pwgen)
    fi
    base64=$(which base64)
    if [[ "$OSTYPE" != "darwin"* ]]; then
        base64="$(which base64) -w0"
    fi
    echo "data:" >> $TMP_DIR/agent-managed/argocd-secret.yaml
    echo "  server.secretkey: $($pwmake 56 | $base64)" >> $TMP_DIR/agent-managed/argocd-secret.yaml
    echo "data:" >> $TMP_DIR/agent-autonomous/argocd-secret.yaml
    echo "  server.secretkey: $($pwmake 56 | $base64)" >> $TMP_DIR/agent-autonomous/argocd-secret.yaml

    # Generate the argocd admin password for the control plane
    if [[ "$gen_admin_pwd" == "true" ]]; then
        echo "-> Generate admin password for the control plane"
        ADMIN_PASSWORD=$($pwmake 56)
        password=$(htpasswd -nb -B a $ADMIN_PASSWORD | cut -c 3-)
        echo "data:" >> $TMP_DIR/control-plane/argocd-secret.yaml
        echo "  admin.password: $(echo $password | $base64)" >> $TMP_DIR/control-plane/argocd-secret.yaml
        echo "  admin.passwordMtime: $(date +%FT%T%Z | $base64)" >> $TMP_DIR/control-plane/argocd-secret.yaml
    fi

    echo "-> Create Argo CD on control plane"

    cluster=control-plane
    namespace=${ARGOCD_PRINCIPAL_NAMESPACE}
    echo "  --> Creating instance in vcluster $cluster"
    kubectl --context vcluster-$cluster create ns $namespace || true

    # Update principal Argo CD ConfigMap 'argocd-cmd-params-cm' to use agent address as redis endpoint, to enable redis proxy functionality
    ARGO_AGENT_IPADDR=$(ip r show default | sed -e 's,.*\ src\ ,,' | sed -e 's,\ metric.*$,,')
    echo "Argo cd agent IPADDR is $ARGO_AGENT_IPADDR"
    if test "$ARGOCD_AGENT_IN_CLUSTER" != ""; then
	    sed -i.bak "s/redis-server-address/argocd-agent-redis-proxy/g" "$TMP_DIR/control-plane/argocd-cmd-params-cm.yaml"
    else
	    sed -i.bak "s/redis-server-address/$ARGO_AGENT_IPADDR/g" "$TMP_DIR/control-plane/argocd-cmd-params-cm.yaml"
    fi

    # Run 'kubectl apply' twice, to avoid the following error that occurs during the first invocation:
    # - 'error: resource mapping not found for name: "default" namespace: "" from "(...)": no matches for kind "AppProject" in version "argoproj.io/v1alpha1"'
    # Use --server-side to avoid "metadata.annotations: Too long" error with large CRDs like applicationsets.argoproj.io
    kubectl --context vcluster-$cluster apply --force-conflicts --server-side -n $namespace -k ${TMP_DIR}/${cluster} || true
    kubectl --context vcluster-$cluster apply --force-conflicts --server-side -n $namespace -k ${TMP_DIR}/${cluster}

    echo "-> Creating Argo CD instances in vclusters"
    for c in $VCLUSTERS_AGENTS; do
        cluster=$(cluster $c)
        namespace=$(namespace $c)
        echo "  --> Creating instance in vcluster $cluster"
        kubectl --context vcluster-$cluster create ns $namespace || true

        # Run 'kubectl apply' twice, to avoid error that occurs during the first invocation (see above for error)
        # Use --server-side to avoid "metadata.annotations: Too long" error with large CRDs like applicationsets.argoproj.io
        kubectl --context vcluster-$cluster apply --force-conflicts --server-side -n $namespace -k ${TMP_DIR}/${cluster} || true
        kubectl --context vcluster-$cluster apply --force-conflicts --server-side -n $namespace -k ${TMP_DIR}/${cluster}
    done

    kubectl --context vcluster-control-plane create ns agent-autonomous || true
    kubectl --context vcluster-control-plane create ns agent-managed || true
    kubectl --context vcluster-agent-managed create ns agent-managed || true

    echo "-> Waiting for all the Argo CD/vCluster pods to be running on vclusters"
    wait_for_pods vcluster-control-plane principal ${ARGOCD_PRINCIPAL_NAMESPACE}
    wait_for_pods vcluster-agent-autonomous agent ${ARGOCD_AUTONOMOUS_NAMESPACE}
    wait_for_pods vcluster-agent-managed agent ${ARGOCD_MANAGED_NAMESPACE}

    # Workaround for web-based terminal issue caused in 3.3.1 release.
    echo "-> Restarting argocd-server on control plane to pick up ConfigMap settings"
    kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} rollout restart deployment/argocd-server
    kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} rollout status deployment/argocd-server --timeout=180s

    echo "-> Service configuration on control plane"
    kubectl --context vcluster-control-plane -n ${ARGOCD_PRINCIPAL_NAMESPACE} get services

    if [[ "$ADMIN_PASSWORD" != "" ]]; then
        echo
        echo "Argo CD Admin Password: $ADMIN_PASSWORD"
        kubectl --context vcluster-control-plane delete --ignore-not-found -n ${ARGOCD_PRINCIPAL_NAMESPACE} secret argocd-initial-admin-secret
        kubectl --context vcluster-control-plane create -n ${ARGOCD_PRINCIPAL_NAMESPACE} secret generic argocd-initial-admin-secret --from-literal="password=${ADMIN_PASSWORD}"
        echo
    fi
}

check_for_openshift


case "$action" in
create)


    kubectl create ns vcluster-agent-managed --context=${initial_context} || true
    kubectl create ns vcluster-control-plane --context=${initial_context} || true
    kubectl create ns vcluster-agent-autonomous --context=${initial_context} || true

    EXTRA_VCLUSTER_PARAMS=""
    EXPOSE_FLAG="--expose"
    CONNECT_FLAG="--connect"

    if [[ "$OPENSHIFT" != "" ]]; then

        # Ensure that the namespaces we are using for our vclusters use our custom SCC (see SCC yaml for details)
        kubectl apply -f ${SCRIPTPATH}/resources/scc-anyuid-seccomp-netbind.yaml

        oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-agent-managed --context=${initial_context}

        oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-control-plane --context=${initial_context}

        oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-agent-autonomous --context=${initial_context}

        EXTRA_VCLUSTER_PARAMS="-f ${SCRIPTPATH}/resources/vcluster.yaml"

        # On OpenShift, each vcluster is exposed via a Route. '--connect' is disabled since its readiness check
        # requires reaching the vcluster directly (e.g. via '--expose' or local port-forwarding),
        # neither of which apply here; wait_for_vcluster_kubeconfig_for_route replaces it below.
        EXPOSE_FLAG=""
        CONNECT_FLAG="--connect=false"
    fi

    echo "-> Creating required vclusters"
    for c in $VCLUSTERS; do
        cluster=$(cluster $c)
        echo "  --> Creating vcluster $cluster"

        CLUSTER_EXTRA_PARAMS="$EXTRA_VCLUSTER_PARAMS"
        if [[ "$OPENSHIFT" != "" ]]; then
            ROUTE_VALUES_FILE=$(expose_vcluster_via_route vcluster-${cluster})
            CLUSTER_EXTRA_PARAMS="$CLUSTER_EXTRA_PARAMS -f ${ROUTE_VALUES_FILE}"
        fi

        vcluster create --context=${initial_context} ${CLUSTER_EXTRA_PARAMS} ${EXPOSE_FLAG} ${CONNECT_FLAG} -n vcluster-${cluster} --kube-config-context-name vcluster-${cluster} vcluster-${cluster}

        if [[ "$OPENSHIFT" != "" ]]; then
            wait_for_vcluster_kubeconfig_for_route vcluster-${cluster} vcluster-${cluster}
        fi

    done
    sleep 2
    if test "$1" != "--skip-argo"; then
       apply
    fi
    ;;
apply)
    apply
    ;;
delete)
    echo "-> Deleting vclusters"
    for c in $VCLUSTERS; do
        cluster=$(cluster $c)
        echo "  --> Deleting vcluster $cluster"
        vcluster delete --context=${initial_context} vcluster-${cluster} || true
    done
    kubectl delete --context=${initial_context} ns vcluster-control-plane || true
    kubectl delete --context=${initial_context} ns vcluster-agent-managed || true
    kubectl delete --context=${initial_context} ns vcluster-agent-autonomous || true

    kubectl config delete-context vcluster-control-plane || true
    kubectl config delete-context vcluster-agent-managed || true
    kubectl config delete-context vcluster-agent-autonomous || true

    ;;
*)
    echo "$0 (create|delete)" >&2
    exit 1
esac
