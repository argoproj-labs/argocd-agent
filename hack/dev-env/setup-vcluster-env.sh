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

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
VCLUSTERS="control-plane:argocd agent-managed:argocd agent-autonomous:argocd"
VCLUSTERS_AGENTS="agent-managed:argocd agent-autonomous:argocd"
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


# wait_for_pods looks all Pods running in k8s context $1, and keeps waiting until running count == $2. 
wait_for_pods() {
    context="$1"
    component="$2"
    case "$component" in
    "principal")
        kubectl --context $context -n argocd rollout status --watch deployments argocd-server
        kubectl --context $context -n argocd rollout status --watch deployments argocd-repo-server
        kubectl --context $context -n argocd rollout status --watch deployments argocd-dex-server
        kubectl --context $context -n argocd rollout status --watch deployments argocd-redis
        ;;
    "agent")
        kubectl --context $context -n argocd rollout status --watch statefulsets argocd-application-controller
        kubectl --context $context -n argocd rollout status --watch deployments argocd-repo-server
        kubectl --context $context -n argocd rollout status --watch deployments argocd-redis
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
    git fetch --depth=1 origin stable
    git checkout FETCH_HEAD
    cd -

    # Comment out 'loadBalancerIP:' lines on OpenShift
    if [[ "$OPENSHIFT" != "" ]]; then
        sed -i.bak -e '/loadBalancerIP/s/^/#/' $TMP_DIR/control-plane/redis-service.yaml
        sed -i.bak -e '/loadBalancerIP/s/^/#/' $TMP_DIR/control-plane/repo-server-service.yaml
        sed -i.bak -e '/loadBalancerIP/s/^/#/' $TMP_DIR/control-plane/server-service.yaml
    else
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/control-plane/redis-service.yaml
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/control-plane/repo-server-service.yaml
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/control-plane/server-service.yaml
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/agent-managed/redis-service.yaml
        sed -i.bak -e "/loadBalancerIP/s/192\.168\.56/${LB_NETWORK}/" $TMP_DIR/agent-autonomous/redis-service.yaml
    fi

    LATEST_RELEASE_TAG=`curl -s "https://api.github.com/repos/argoproj/argo-cd/releases/latest" | jq -r .tag_name`
    sed -i.bak -e "s/LatestReleaseTag/${LATEST_RELEASE_TAG}/" $TMP_DIR/control-plane/kustomization.yaml
    sed -i.bak -e "s/LatestReleaseTag/${LATEST_RELEASE_TAG}/" $TMP_DIR/agent-autonomous/kustomization.yaml
    sed -i.bak -e "s/LatestReleaseTag/${LATEST_RELEASE_TAG}/" $TMP_DIR/agent-managed/kustomization.yaml

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
    namespace=argocd
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
    kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster} || true
    kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster}

    if [[ "$OPENSHIFT" != "" ]]; then

        # echo "-> Waiting for Redis load balancer on control plane Argo CD"

        # while [ true ]
        # do
        #     REDIS_ADDR=`kubectl --context vcluster-control-plane -n argocd   get service/argocd-redis -o json | jq -r '.status.loadBalancer.ingress[0].hostname'`

        #     if [[ "$REDIS_ADDR" != "" ]] && [[ "$REDIS_ADDR" != "null" ]]; then
        #         break
        #     fi

        #     sleep 2
        # done

        echo "-> Waiting for repo-server load balancer on control plane Argo CD"

        while [ true ]
        do
            REPO_SERVER_ADDR=`kubectl --context vcluster-control-plane -n argocd   get service/argocd-repo-server -o json | jq -r '.status.loadBalancer.ingress[0].hostname'`

            if [[ "$REPO_SERVER_ADDR" != "" ]] && [[ "$REPO_SERVER_ADDR" != "null" ]]; then
                break
            fi
            sleep 2

        done

    else
        # For all other cases, use hardcoded values
        REPO_SERVER_ADDR="${LB_NETWORK}.222"
        # REDIS_ADDR="${LB_NETWORK}.221"
    fi

    REDIS_ADDR=argocd-redis # Now that redis proxy is implemented, agent can connect to its own redis (I've left the existing logic above, for debugging purposes, to be removed at a later time)

    echo "Redis on control plane: $REDIS_ADDR"
    echo "Repo server URL on control plane: $REPO_SERVER_ADDR"

    # Update the Argo CD repo-server/redis addresses that agent-managed Argo CD instance connects to
    sed -i.bak "s/repo-server-address/$REPO_SERVER_ADDR/g" "$TMP_DIR/agent-managed/argocd-cmd-params-cm.yaml"
    sed -i.bak "s/redis-server-address/$REDIS_ADDR/g" "$TMP_DIR/agent-managed/argocd-cmd-params-cm.yaml"

    echo "-> Creating Argo CD instances in vclusters"
    for c in $VCLUSTERS_AGENTS; do
        cluster=$(cluster $c)
        namespace=$(namespace $c)
        echo "  --> Creating instance in vcluster $cluster"
        kubectl --context vcluster-$cluster create ns $namespace || true

        # Run 'kubectl apply' twice, to avoid error that occurs during the first invocation (see above for error)
        kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster} || true
        kubectl --context vcluster-$cluster apply -n $namespace -k ${TMP_DIR}/${cluster}
    done

    kubectl --context vcluster-control-plane create ns agent-autonomous || true
    kubectl --context vcluster-control-plane create ns agent-managed || true
    kubectl --context vcluster-agent-managed create ns agent-managed || true

    echo "-> Waiting for all the Argo CD/vCluster pods to be running on vclusters"
    wait_for_pods vcluster-control-plane principal
    wait_for_pods vcluster-agent-autonomous agent
    wait_for_pods vcluster-agent-managed agent

    echo "-> Service configuration on control plane"
    kubectl --context vcluster-control-plane -n argocd get services

    if [[ "$ADMIN_PASSWORD" != "" ]]; then
        echo
        echo "Argo CD Admin Password: $ADMIN_PASSWORD"
        kubectl --context vcluster-control-plane delete --ignore-not-found -n argocd secret argocd-initial-admin-secret
        kubectl --context vcluster-control-plane create -n argocd secret generic argocd-initial-admin-secret --from-literal="password=${ADMIN_PASSWORD}"
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

    if [[ "$OPENSHIFT" != "" ]]; then

        # Ensure that the namespaces we are using for our vclusters use our custom SCC (see SCC yaml for details)
        kubectl apply -f ${SCRIPTPATH}/resources/scc-anyuid-seccomp-netbind.yaml

        oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-agent-managed --context=${initial_context}

        oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-control-plane --context=${initial_context}

        oc adm policy add-scc-to-group anyuid-seccomp-netbind system:serviceaccounts:vcluster-agent-autonomous --context=${initial_context}

        EXTRA_VCLUSTER_PARAMS="-f ${SCRIPTPATH}/resources/vcluster.yaml"
    fi

    echo "-> Creating required vclusters"
    for c in $VCLUSTERS; do
        cluster=$(cluster $c)
        echo "  --> Creating vcluster $cluster"
        vcluster create --context=${initial_context} ${EXTRA_VCLUSTER_PARAMS} -n vcluster-${cluster} --expose --kube-config-context-name vcluster-${cluster} vcluster-${cluster}

        # I found a sleep statement here was beneficial to allow time for the load balancer to become available. If we find this is not required, these commented out lines should be removed.
        # if [[ "$OPENSHIFT" != "" ]]; then
        #   sleep 60
        # fi
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
