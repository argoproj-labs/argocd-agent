#!/bin/bash
# Recorded Demo: Declarative ClusterProfile -> argocd-agent Registration
# Press ENTER to advance through each step.
#
# Every command and every line of output below was captured verbatim from a
# real, from-scratch run of setup-kind-poc.sh against two Kind clusters
# (argocd-hub, argocd-agent1) on 2026-07-09. Nothing here is simulated data;
# resource ages, JWTs, and certificate bytes are real (the JWT/cert have been
# truncated only for terminal readability, never fabricated). See ../README.md
# for the full write-up. This script itself has no dependency on demo-magic,
# asciinema, or any other external tool/library -- it is a plain, self-contained
# bash script, in the same spirit as ../../demo.sh.

BOLD="\033[1m"
GREEN="\033[0;32m"
CYAN="\033[0;36m"
YELLOW="\033[0;33m"
GREY="\033[0;90m"
RED="\033[0;31m"
RESET="\033[0m"
BLUE="\033[0;34m"

PROMPT="${GREEN}\$ ${RESET}"
TYPE_DELAY="${DEMO_TYPE_DELAY:-0}"

# DEMO_AUTOPLAY=1 turns "press ENTER to advance" into a fixed pause, so this
# script can be driven unattended (e.g. by `asciinema rec` when producing
# demo.gif/demo.mp4 for sharing). Interactive use is unaffected by default.
AUTOPLAY="${DEMO_AUTOPLAY:-0}"
STEP_DELAY="${DEMO_STEP_DELAY:-1.6}"

type_cmd() {
  local cmd="$1"
  printf "${PROMPT}"
  for ((i=0; i<${#cmd}; i++)); do
    printf "${BOLD}%s${RESET}" "${cmd:$i:1}"
    sleep "${TYPE_DELAY}"
  done
  echo ""
}

show_output() {
  echo -e "$1"
}

wait_for_enter() {
  if [[ "$AUTOPLAY" == "1" ]]; then
    sleep "$STEP_DELAY"
  else
    read -rs
  fi
}

section() {
  echo ""
  echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${CYAN}  $1${RESET}"
  echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo ""
}

comment() {
  echo -e "${GREY}# $1${RESET}"
}

step() {
  local cmd="$1"
  local output="$2"
  wait_for_enter
  type_cmd "$cmd"
  sleep 0.3
  show_output "$output"
}

clear
echo ""
echo -e "${BOLD}${CYAN}============================================================${RESET}"
echo -e "${BOLD}${CYAN}  Declarative ClusterProfile -> argocd-agent Registration${RESET}"
echo -e "${BOLD}${CYAN}  One kubectl apply. Zero argocd-agentctl. Zero hand-crafted Secrets.${RESET}"
echo -e "${BOLD}${CYAN}============================================================${RESET}"
echo ""
echo -e "${GREY}  Press ENTER to advance through each step${RESET}"
echo ""

###############################################################################
section "PART 1: The Problem Today"
###############################################################################

comment "We have a hub cluster (argocd-hub) running Argo CD + the argocd-agent"
comment "principal, and a spoke cluster (argocd-agent1) running the argocd-agent"
comment "agent in managed mode. They are already connected at the mTLS/gRPC"
comment "transport level -- that part is orthogonal to what we're fixing here."

step 'kubectl --context kind-argocd-hub -n argocd logs deploy/argocd-agent-principal --tail=200 | grep "agent connected"' \
'time="2026-07-09T04:34:29Z" level=info msg="An agent connected to the subscription stream" client=agent-a method=Subscribe'

comment "But Argo CD itself has no registered cluster for agent-a yet:"

step 'kubectl --context kind-argocd-hub -n argocd get clusterprofile' \
'No resources found in argocd namespace.'

step 'kubectl --context kind-argocd-hub -n argocd get secret -l argocd.argoproj.io/secret-type=cluster' \
'No resources found in argocd namespace.'

comment "Today, registering agent-a as a usable Argo CD destination means"
comment "either 'argocd-agentctl agent create agent-a ...' (imperative CLI),"
comment "or waiting for the agent to self-register on first connect."
comment "Neither is something you can kubectl apply, GitOps, or diff in a PR."

###############################################################################
section "PART 2: One Declarative Object"
###############################################################################

comment "The entire imperative surface we're proposing is this ClusterProfile:"

step 'cat docs/clusterprofile-integration/manifests/sample-clusterprofile.yaml' \
'apiVersion: multicluster.x-k8s.io/v1alpha1
kind: ClusterProfile
metadata:
  name: agent-a
  namespace: argocd
  labels:
    x-k8s.io/cluster-manager: argocd-agent
    argocd-agent.argoproj-labs.io/agent-name: agent-a
spec:
  clusterManager:
    name: argocd-agent
  displayName: agent-a'

step 'kubectl --context kind-argocd-hub apply -f docs/clusterprofile-integration/manifests/sample-clusterprofile.yaml' \
'clusterprofile.multicluster.x-k8s.io/agent-a created'

step 'kubectl --context kind-argocd-hub -n argocd get clusterprofile agent-a' \
'NAME      AGE
agent-a   0s'

echo ""
echo -e "${GREEN}${BOLD}That's it. One object. Everything below happens automatically.${RESET}"
echo ""

###############################################################################
section "PART 3: What Happened Automatically"
###############################################################################

comment "A new controller, clusterprofile-agent-driver, reconciled it within seconds:"

step 'kubectl --context kind-argocd-hub -n argocd get clusterprofile agent-a -o jsonpath="{.status.conditions}"' \
'[{"lastTransitionTime":"2026-07-09T04:38:42Z","message":"Resource-proxy token minted and AccessProvider \"argocd-agent\" published for agent \"agent-a\"","observedGeneration":1,"reason":"TokenIssued","status":"True","type":"ArgoCDAgentRegistered"}]'

comment "It minted a non-expiring resource-proxy JWT (using the principal's own"
comment "signing key) and stored it in a companion Secret, co-located with the"
comment "ClusterProfile and owned by it:"

step 'kubectl --context kind-argocd-hub -n argocd get secret agent-a-argocd-agent-token' \
'NAME                         TYPE     DATA   AGE
agent-a-argocd-agent-token   Opaque   1      3s'

comment "...and published an AccessProvider pointing at the resource proxy:"

step 'kubectl --context kind-argocd-hub -n argocd get clusterprofile agent-a -o jsonpath="{.status.accessProviders[0].name}{\"\n\"}{.status.accessProviders[0].cluster.server}{\"\n\"}"' \
'argocd-agent
https://argocd-agent-resource-proxy.argocd.svc.cluster.local:9090?agentName=agent-a'

echo ""
echo -e "${YELLOW}${BOLD}Now watch: clusterprofile-integration-for-argocd -- completely unmodified,${RESET}"
echo -e "${YELLOW}${BOLD}pulled straight from its upstream main branch -- picks this up next.${RESET}"
echo ""

step 'kubectl --context kind-argocd-hub -n argocd get secret cluster-agent-a' \
'NAME              TYPE     DATA   AGE
cluster-agent-a   Opaque   3      12s'

comment "That's a real, normal Argo CD cluster Secret. Decoded:"

step 'kubectl --context kind-argocd-hub -n argocd get secret cluster-agent-a -o jsonpath="{.data.server}" | base64 -d' \
'https://argocd-agent-resource-proxy.argocd.svc.cluster.local:9090?agentName=agent-a'

step 'kubectl --context kind-argocd-hub -n argocd get secret cluster-agent-a -o jsonpath="{.data.config}" | base64 -d | jq .' \
'{
  "tlsClientConfig": {
    "insecure": false,
    "caData": "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t...(2512 more bytes, truncated for display)"
  },
  "execProviderConfig": {
    "command": "/custom-tools/argocd-agent-creds",
    "apiVersion": "client.authentication.k8s.io/v1beta1",
    "provideClusterInfo": true,
    "config": {
      "agentName": "agent-a",
      "tokenSecretKey": "token",
      "tokenSecretName": "agent-a-argocd-agent-token",
      "tokenSecretNamespace": "argocd"
    }
  }
}'

echo ""
echo -e "${GREEN}${BOLD}Zero manual Argo CD Secret. Zero argocd-agentctl commands. The unmodified${RESET}"
echo -e "${GREEN}${BOLD}CPI controller did this, exactly the way it already does for OCM.${RESET}"
echo ""

###############################################################################
section "PART 4: The Principal Notices"
###############################################################################

comment "argocd-agent's principal has its own, separate, pre-existing in-memory"
comment "cluster informer that maps agent names to Argo CD clusters. Here it is"
comment "flipping from 'not mapped' to 'mapped', at the exact moment the Secret"
comment "above appeared -- captured live from the same run:"

step 'kubectl --context kind-argocd-hub -n argocd logs deploy/argocd-agent-principal --tail=100 | grep agent-a' \
'time="2026-07-09T04:38:28Z" level=error msg="agent agent-a is not mapped to any cluster" component=ClusterManager
time="2026-07-09T04:38:28Z" level=error msg="Could not process agent receiver queue" client=agent-a error="agent agent-a is not mapped to any cluster" module=EventProcessor queueName=agent-a
time="2026-07-09T04:38:39Z" level=error msg="agent agent-a is not mapped to any cluster" component=ClusterManager
time="2026-07-09T04:38:39Z" level=error msg="Could not process agent receiver queue" client=agent-a error="agent agent-a is not mapped to any cluster" module=EventProcessor queueName=agent-a
time="2026-07-09T04:38:42Z" level=info msg="Mapped cluster agent-a to agent agent-a" component=ClusterManager'

echo ""
echo -e "${GREEN}${BOLD}The moment the Secret landed, the principal mapped it. No restart needed.${RESET}"
echo ""

###############################################################################
section "PART 5: Proving the Credential Path Actually Works"
###############################################################################

comment "The new argocd-agent-creds exec plugin is what argocd-server shells out to"
comment "whenever it needs a credential for cluster-agent-a. Let's call it exactly"
comment "the way client-go would, for real, inside the running argocd-server pod:"

step 'POD=$(kubectl --context kind-argocd-hub -n argocd get pod -l app.kubernetes.io/name=argocd-server -o jsonpath="{.items[0].metadata.name}")
kubectl --context kind-argocd-hub -n argocd exec "$POD" -c argocd-server -- \
  env KUBERNETES_EXEC_INFO="{\"apiVersion\":\"client.authentication.k8s.io/v1beta1\",\"kind\":\"ExecCredential\",\"spec\":{\"cluster\":{\"server\":\"https://argocd-agent-resource-proxy.argocd.svc.cluster.local:9090?agentName=agent-a\",\"config\":{\"agentName\":\"agent-a\",\"tokenSecretKey\":\"token\",\"tokenSecretName\":\"agent-a-argocd-agent-token\",\"tokenSecretNamespace\":\"argocd\"}}}}" \
  /custom-tools/argocd-agent-creds' \
'{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1beta1","spec":{"interactive":false},"status":{"token":"eyJhbGciOiJSUzUxMiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJhcmdvY2QtYWdlbnQtc2VydmVyIiwic3ViIjoiYWdlbnQtYSIsImF1ZCI6WyJhcmdvY2QtYWdlbnQtc2VydmVyLXJlc291cmNlLXByb3h5Il0sIm5iZiI6MTc4MzU3MTkyMiwiaWF0IjoxNzgzNTcxOTIyLCJqdGkiOiJiZGNkYjg2OS0wZGJiLTQzZDUtOThkYS02YmRiYTU3MDJkYWYifQ.oOrb139eG_qMOfCWHDJ-kss4c2PV9135zY8Qb27KzgKuTYuYgrC-KRBSQLmvqfH6WiT4btsr-Mrfs8xUT-bKVA8cwbIZ18es1qR90-sh8LXl9J6jjnlLuYj982qhIsaVdXDA2wWJSrgnIPCF0NgZpL_KThkYbXOxCELvdpovZ3ypIA8DxuFYCEwl7BRqL3qCdzbVwVKADAiyvI_uxwZHbb2DH5OwHq5RpngxqfODT8bTPLmHejJ3VNQ8vdCRZItSgb5BYfZmxNjpm6OR25XZVnbNsAWQzBinj2Ft1M7svC1pzJq-TAHoNi3NtOiIbAd2QbSjkczWNYS6b66dPuxWgIvmOEe-N35KO1le_TC52Yc1GVerJVs6hfXzreT6MkPoUHWzWl2N15OX9XIpAfw0ylTghLLFTIS8j3VsRgsdjjfR1mKgrsId_WFxTOIzs5GOUbNga8WIYmOhaKIc9yqnFXs6tNFqhXdeoliRIzSf0dbk_trUpaRtImze9-zMprKDWTvBfwHGe-fRi6xF5Ro81cQLd9OwakeTToHG9lvlQnZqvJvXRiUDPdesNqKpw0ysd5SHe200W31QNmh6uYOTtOthPxo1X9d4nu3I9fZcgNQYf8Uf5CUZ4pU9x6aOPPGDieOZ3qCKJg46iGD0OWbvYl_34hWyY2ZiUDQP2S6sFhA"}}'

echo ""
echo -e "${GREEN}${BOLD}That JWT is real, signed by the principal's own key, valid right now.${RESET}"
echo ""

###############################################################################
section "PART 6: A Real Application, Deployed Through It"
###############################################################################

comment "Now let's actually use agent-a as a destination:"

step 'cat docs/clusterprofile-integration/manifests/sample-app.yaml' \
'apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: guestbook
  namespace: agent-a
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps
    targetRevision: HEAD
    path: kustomize-guestbook
  destination:
    name: agent-a
    namespace: guestbook
  syncPolicy:
    automated:
      selfHeal: true
    syncOptions:
      - CreateNamespace=true'

step 'kubectl --context kind-argocd-hub apply -f docs/clusterprofile-integration/manifests/sample-app.yaml' \
'application.argoproj.io/guestbook created'

step 'kubectl --context kind-argocd-hub -n agent-a get application guestbook' \
'NAME        SYNC STATUS   HEALTH STATUS
guestbook   Synced        Healthy'

comment "And on the spoke cluster, for real:"

step 'kubectl --context kind-argocd-agent1 -n guestbook get pods,svc,deploy' \
'NAME                                          READY   STATUS    RESTARTS   AGE
pod/kustomize-guestbook-ui-5d6468fd55-hdb8g   1/1     Running   0          4m37s

NAME                             TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)   AGE
service/kustomize-guestbook-ui   ClusterIP   10.108.175.17   <none>        80/TCP    4m37s

NAME                                     READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/kustomize-guestbook-ui   1/1     1            1           4m37s'

echo ""
echo -e "${GREEN}${BOLD}A real Deployment, running on a real spoke cluster, reached purely${RESET}"
echo -e "${GREEN}${BOLD}through a ClusterProfile an operator kubectl-applied a minute ago.${RESET}"
echo ""

###############################################################################
section "SUMMARY"
###############################################################################

echo -e "${BOLD}What we demonstrated:${RESET}"
echo ""
echo -e "  ${CYAN}1. One declarative object${RESET}"
echo -e "     kubectl apply -f sample-clusterprofile.yaml -- no argocd-agentctl,"
echo -e "     no hand-crafted Secret, no self-registration wait."
echo ""
echo -e "  ${CYAN}2. A small, additive bridge controller${RESET}"
echo -e "     clusterprofile-agent-driver mints a token and publishes an"
echo -e "     AccessProvider -- new code, but zero changes to argocd-agent."
echo ""
echo -e "  ${CYAN}3. Zero changes to clusterprofile-integration-for-argocd${RESET}"
echo -e "     The unmodified, upstream-main CPI controller turned that"
echo -e "     AccessProvider into a normal Argo CD cluster Secret, just like"
echo -e "     it already does for OCM."
echo ""
echo -e "  ${CYAN}4. A real credential path${RESET}"
echo -e "     The argocd-agent-creds exec plugin fetched a live, valid JWT"
echo -e "     inside the actual argocd-server container."
echo ""
echo -e "  ${CYAN}5. A real application, on a real spoke cluster${RESET}"
echo -e "     guestbook: Synced / Healthy, running pods and all -- reached"
echo -e "     entirely through the ClusterProfile we declared in Part 2."
echo ""
echo -e "${BOLD}${GREEN}Thank you!${RESET}"
echo ""
wait_for_enter
