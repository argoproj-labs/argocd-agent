#/bin/bash

# getExternalLoadBalancerIP will set EXTERNAL_IP with the load balancer hostname from the specified Service
getExternalLoadBalancerIP() {
  SERVICE_NAME=$1

  MAX_ATTEMPTS=120

  for ((i=1; i<=MAX_ATTEMPTS; i++)); do
    
    echo ""
    EXTERNAL_IP=$(kubectl get svc $SERVICE_NAME $K8S_CONTEXT $K8S_NAMESPACE -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')

    if [ -n "$EXTERNAL_IP" ]; then
      echo "External IP is $EXTERNAL_IP"
      break
    else
      echo "External IP not yet available, attempting again in 5 seconds..."
      sleep 5
    fi
  done

  if [ $i -gt $MAX_ATTEMPTS ]; then
    echo "Failed to obtain external IP after $MAX_ATTEMPTS attempts."
    exit 1
  fi

}

# Get hostname of control-plane redis
K8S_CONTEXT="--context=vcluster-control-plane"
K8S_NAMESPACE="-n argocd"
getExternalLoadBalancerIP "argocd-redis"
export ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS="$EXTERNAL_IP:6379"


# Get hostname of agent-managed redis
K8S_CONTEXT="--context=vcluster-agent-managed"
K8S_NAMESPACE="-n argocd"
getExternalLoadBalancerIP "argocd-redis"
export MANAGED_AGENT_REDIS_ADDR="$EXTERNAL_IP:6379"

# Get hostname of agent-autonomous redis
K8S_CONTEXT="--context=vcluster-agent-autonomous"
K8S_NAMESPACE="-n argocd"
getExternalLoadBalancerIP "argocd-redis"
export AUTONOMOUS_AGENT_REDIS_ADDR="$EXTERNAL_IP:6379"


export REDIS_PASSWORD=$(kubectl get secret argocd-redis --context=vcluster-agent-managed $K8S_NAMESPACE -o jsonpath='{.data.auth}' | base64 --decode)

goreman -exit-on-stop=false -f hack/dev-env/Procfile.e2e start

