#!/bin/bash

SCRIPTPATH="$(
    cd -- "$(dirname "$0")" >/dev/null 2>&1 || exit
    pwd -P
)"

DOCKER_BIN=${DOCKER_BIN:-"docker"}


TEMP_DIR=$(mktemp -d)

echo "Using temporary directory: $TEMP_DIR"

cp -R $SCRIPTPATH/. $TEMP_DIR

# Call 'rathole  --genkey x25519' to generate temporary public/private key, and store in env vars
generatePublicAndPrivateKey() {

  TEMP_FILE=$(mktemp)

  "$DOCKER_BIN" run --rm "quay.io/jgwest-redhat/rathole:v0.5.0@sha256:53999f80b69f9a5020e19e9c9be90fc34b973d9bd822d4fd44b968f2ebe0845f"  --genkey x25519 > $TEMP_FILE
  if [ $? -ne 0 ]; then
    echo "Error: docker run command failed."
    exit 1
  fi

  # Rathole --genkey command produces text in this format:
  # Private Key:
  # XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX=

  # Public Key:
  # XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX=


  KEY_TEXT=$(<$TEMP_FILE)

  PRIVATE_KEY=`echo $KEY_TEXT | awk '{print $3}'`
  PUBLIC_KEY=`echo $KEY_TEXT | awk '{print $6}'`

}

# Store public/private key in client/server configuration, and generate auth token
configureClientAndServerToml() {

  generatePublicAndPrivateKey

  AUTH_TOKEN=$(openssl rand -hex 32)

  # Using a custom delimiter '#' because private/public key often contain '/' character, which sed doesn't like
  sed -i.bak "s#LOCAL_PRIVATE_KEY#$PRIVATE_KEY#g" $TEMP_DIR/server/server.toml
  sed -i.bak "s#REMOTE_PUBLIC_KEY#$PUBLIC_KEY#g" $TEMP_DIR/client/client.toml

  sed -i.bak "s#AUTHENTICATION_TOKEN#$AUTH_TOKEN#g" $TEMP_DIR/client/client.toml
  sed -i.bak "s#AUTHENTICATION_TOKEN#$AUTH_TOKEN#g" $TEMP_DIR/server/server.toml

}

# getExternalLoadBalancerIP will set EXTERNAL_IP with the load balancer hostname
getExternalLoadBalancerIP() {
  SERVICE_NAME=$1

  MAX_ATTEMPTS=120

  for ((i=1; i<=MAX_ATTEMPTS; i++)); do
    
    echo ""
    EXTERNAL_IP=$(kubectl get svc $SERVICE_NAME $K8S_CONTEXT_CONTROL_PLANE $K8S_NAMESPACE -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')

    if [ -n "$EXTERNAL_IP" ]; then
      echo "External IP is $EXTERNAL_IP"
      break
    else
      echo "External IP for $SERVICE_NAME not yet available, attempting again in 5 seconds..."
      sleep 5
    fi
  done

  if [ $i -gt $MAX_ATTEMPTS ]; then
    echo "Failed to obtain external IP after $MAX_ATTEMPTS attempts."
    exit 1
  fi

}

# -----------------------------------------------------------------------------

configureClientAndServerToml

# Create Rathole Deployment and Services in Argo CD principal namespace

echo ""
echo "Installing Rathole on K8s"

K8S_CONTEXT_CONTROL_PLANE="--context=vcluster-control-plane"
K8S_NAMESPACE="-n argocd"

kubectl apply $K8S_CONTEXT_CONTROL_PLANE $K8S_NAMESPACE -k $TEMP_DIR/server 


# Wait for rathole-container-external load balancer hostname
getExternalLoadBalancerIP "rathole-container-external"

sed -i.bak "s#EXTERNAL-HOSTNAME#$EXTERNAL_IP#g" $TEMP_DIR/client/client.toml

NEW_VALUE_BASE64=$(echo -n 'https://rathole-container-internal:9090?agentName=agent-managed' | base64  -w 0)

echo ""
echo "Patching cluster-agent-managed secret on vcluster-control-plane"

# Replace the .data.server field of the Secret
kubectl $K8S_CONTEXT_CONTROL_PLANE $K8S_NAMESPACE patch secret cluster-agent-managed  --type='json' -p='[{"op": "replace", "path": "/data/server", "value":"'"$NEW_VALUE_BASE64"'"}]'


# Extract .data.config
CONFIG_FIELD_VALUE=`kubectl $K8S_CONTEXT_CONTROL_PLANE $K8S_NAMESPACE get secret cluster-agent-managed -o jsonpath='{.data.config}' | base64 --decode`

# remove .data.config.tlsClientConfig.caData and set '.data.config.tlsClientConfig.insecure = true'
TLS_CLIENT_CONFIG=`echo "$CONFIG_FIELD_VALUE" | jq -r '.tlsClientConfig' | jq 'del(.caData)' | jq '.insecure = true' `
CONFIG_FIELD_VALUE=`echo $CONFIG_FIELD_VALUE | jq ".tlsClientConfig = $TLS_CLIENT_CONFIG"`

CONFIG_FIELD_VALUE_BASE64=$(echo -n "$CONFIG_FIELD_VALUE" | base64 -w 0)

# Patch the secret with the new .data.config value
kubectl $K8S_CONTEXT_CONTROL_PLANE $K8S_NAMESPACE patch secret cluster-agent-managed  --type='json' -p='[{"op": "replace", "path": "/data/config", "value":"'"$CONFIG_FIELD_VALUE_BASE64"'"}]'

echo ""
echo "Patching ConfigMap 'argocd-cmd-params-cm' to redirect redis to tunnel"
kubectl $K8S_CONTEXT_CONTROL_PLANE $K8S_NAMESPACE patch configmap argocd-cmd-params-cm --type json --patch '[{"op": "add", "path": "/data/redis.server", "value": "rathole-container-internal:6379"}]'

echo ""
echo "Patching Service 'argocds-redis' to enable type: LoadBalancer, on vcluster-agent-managed"
kubectl --context=vcluster-agent-managed $K8S_NAMESPACE patch service argocd-redis -p '{"spec":{"type":"LoadBalancer"}}'

echo ""
echo "Patching Service 'argocds-redis' to enable type: LoadBalancer, on vcluster-agent-autonomous"
kubectl --context=vcluster-agent-autonomous $K8S_NAMESPACE patch service argocd-redis -p '{"spec":{"type":"LoadBalancer"}}'


echo ""
echo "Restarting all pods in argocd NS"
kubectl $K8S_CONTEXT_CONTROL_PLANE $K8S_NAMESPACE delete pods --all

echo ""
echo "* Starting the rathole local client."
echo "* - This may initially report an error while waiting for LoadBalancer service ('Failed to run the control channel: (...): Name or service not known. '), but it will keep trying until connection is established."
echo "* - You can also safely ignore this warning: 'Failed to run the data channel: Failed to read cmd: early eof'"
echo "* - This message indicates you need to run 'make start-e2e', or that it is not currently running: 'Failed to run the data channel: Failed to connect to 127.0.0.1:6379: Connection refused (os error 111)'"
echo ""
echo "CTRL-C / Command-C to terminate rathole tunnel."
$TEMP_DIR/client/run.sh

