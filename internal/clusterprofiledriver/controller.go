// Copyright 2024 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clusterprofiledriver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientcmdv1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"k8s.io/client-go/util/retry"
	clusterinventory "sigs.k8s.io/cluster-inventory-api/apis/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/argoproj-labs/argocd-agent/internal/issuer"
)

// conditionAgentRegistered is set on ClusterProfile.status.conditions once
// the driver has successfully written the AccessProvider entry and its
// companion token Secret.
const conditionAgentRegistered = "ArgoCDAgentRegistered"

// Reconciler reconciles ClusterProfile objects whose
// spec.clusterManager.name is "argocd-agent", by minting a resource-proxy
// bearer token for the agent and publishing the corresponding
// status.accessProviders entry that clusterprofile-integration-for-argocd
// turns into an Argo CD cluster Secret.
//
// The Reconciler deliberately does NOT talk to the argocd-agent principal's
// gRPC API, does NOT create Argo CD Secrets itself, and does NOT modify
// argocd-agent's or clusterprofile-integration-for-argocd's existing
// behavior. It is purely an additive, optional bridge.
type Reconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	// Issuer is used to mint non-expiring resource-proxy JWTs. It must be
	// constructed with the same signing key used by the argocd-agent
	// principal (secret argocd-agent-jwt, key jwt.key), so that tokens
	// minted here validate against principal.ValidateResourceProxyToken.
	Issuer issuer.Issuer

	// ResourceProxyAddress is the host:port at which Argo CD (running on the
	// principal) can reach the argocd-agent-resource-proxy service, e.g.
	// "argocd-agent-resource-proxy.argocd.svc.cluster.local:9090".
	ResourceProxyAddress string

	// ResourceProxyCAData is the PEM-encoded CA bundle that signed the
	// resource proxy's server TLS certificate (secret argocd-agent-ca,
	// key ca.crt). It is embedded verbatim into every AccessProvider this
	// controller writes.
	ResourceProxyCAData []byte
}

//+kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=clusterprofiles,verbs=get;list;watch
//+kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=clusterprofiles/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("clusterprofile", req.NamespacedName)

	var cp clusterinventory.ClusterProfile
	if err := r.Get(ctx, req.NamespacedName, &cp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch ClusterProfile")
		return ctrl.Result{}, err
	}

	if cp.Spec.ClusterManager.Name != ClusterManagerName {
		// Not ours; some other driver (or a human) owns this ClusterProfile.
		return ctrl.Result{}, nil
	}

	if !cp.DeletionTimestamp.IsZero() {
		// The companion Secret carries an OwnerReference to this
		// ClusterProfile, so Kubernetes garbage collection deletes it for
		// us. Nothing else to clean up.
		return ctrl.Result{}, nil
	}

	agentName := cp.Name

	// Self-heal the label the argocd-agent principal's own cluster informer
	// requires on the *generated* Argo CD Secret (see the comment on
	// clusterAgentMappingLabel). clusterprofile-integration-for-argocd
	// copies ClusterProfile labels verbatim onto that Secret, so setting it
	// here is enough; we never touch the Secret it creates.
	if cp.Labels[clusterAgentMappingLabel] != agentName {
		if err := r.ensureAgentMappingLabel(ctx, req, agentName); err != nil {
			log.Error(err, "unable to set cluster-agent mapping label on ClusterProfile")
			return ctrl.Result{}, err
		}
		// The label update above changed the object's resourceVersion; the
		// resulting watch event will trigger a fresh reconcile that picks
		// up the rest of the work against an up-to-date object.
		return ctrl.Result{}, nil
	}

	token, err := r.ensureTokenSecret(ctx, &cp, agentName)
	if err != nil {
		log.Error(err, "unable to ensure companion token secret")
		return ctrl.Result{}, err
	}
	_ = token // token is stored in the Secret; not needed further here.

	if err := r.updateAccessProvider(ctx, &cp, agentName); err != nil {
		log.Error(err, "unable to update ClusterProfile status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// ensureAgentMappingLabel patches ClusterProfile.metadata.labels so that
// clusterAgentMappingLabel is present and equal to agentName. Retries on
// update conflicts by re-fetching the object, since this may race with
// other writers (e.g. our own status update from a previous reconcile).
func (r *Reconciler) ensureAgentMappingLabel(ctx context.Context, req ctrl.Request, agentName string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cp clusterinventory.ClusterProfile
		if err := r.Get(ctx, req.NamespacedName, &cp); err != nil {
			return err
		}
		if cp.Labels[clusterAgentMappingLabel] == agentName {
			return nil
		}
		if cp.Labels == nil {
			cp.Labels = map[string]string{}
		}
		cp.Labels[clusterAgentMappingLabel] = agentName
		return r.Update(ctx, &cp)
	})
}

// tokenSecretName derives the companion Secret name for a given ClusterProfile.
func tokenSecretName(agentName string) string {
	return agentName + TokenSecretNameSuffix
}

// ensureTokenSecret makes sure a companion Secret holding a resource-proxy
// bearer token exists in the same namespace as the ClusterProfile. If the
// Secret already exists, its token is reused (tokens do not expire, so
// there is no need to rotate them on every reconcile).
func (r *Reconciler) ensureTokenSecret(
	ctx context.Context,
	cp *clusterinventory.ClusterProfile,
	agentName string,
) (string, error) {
	secretName := tokenSecretName(agentName)

	var existing corev1.Secret
	err := r.Get(ctx, client.ObjectKey{Namespace: cp.Namespace, Name: secretName}, &existing)
	if err == nil {
		if tok, ok := existing.Data[TokenSecretKey]; ok && len(tok) > 0 {
			return string(tok), nil
		}
		// Fall through and repopulate a Secret that exists but is missing data.
	} else if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("failed to get token secret: %w", err)
	}

	token, err := r.Issuer.IssueResourceProxyToken(agentName)
	if err != nil {
		return "", fmt.Errorf("failed to mint resource-proxy token: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cp.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":             "clusterprofile-agent-driver",
				"argocd-agent.argoproj-labs.io/agent-name": agentName,
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[TokenSecretKey] = []byte(token)
		return controllerutil.SetOwnerReference(cp, secret, r.Scheme)
	})
	if err != nil {
		return "", fmt.Errorf("failed to create or update token secret: %w", err)
	}
	return token, nil
}

// updateAccessProvider upserts this driver's AccessProvider entry into
// ClusterProfile.status.accessProviders, and sets an informational
// condition.
func (r *Reconciler) updateAccessProvider(
	ctx context.Context,
	cp *clusterinventory.ClusterProfile,
	agentName string,
) error {
	ext := ExecExtension{
		AgentName:            agentName,
		TokenSecretName:      tokenSecretName(agentName),
		TokenSecretNamespace: cp.Namespace,
		TokenSecretKey:       TokenSecretKey,
	}
	extBytes, err := json.Marshal(ext)
	if err != nil {
		return fmt.Errorf("failed to marshal exec extension: %w", err)
	}

	// Primary channel (see the comment on additionalEnvVarsExtensionKey):
	// these become the argocd-agent-creds process's environment variables
	// unconditionally, regardless of whether Argo CD happened to set
	// ProvideClusterInfo/Cluster on a given ExecCredential request.
	envVars := map[string]string{
		EnvAgentName:            agentName,
		EnvTokenSecretName:      tokenSecretName(agentName),
		EnvTokenSecretNamespace: cp.Namespace,
		EnvTokenSecretKey:       TokenSecretKey,
		EnvMTLSSecretName:       SharedClientCertSecretName,
		EnvMTLSSecretNamespace:  cp.Namespace,
	}
	envBytes, err := json.Marshal(envVars)
	if err != nil {
		return fmt.Errorf("failed to marshal exec additional-envs extension: %w", err)
	}

	server := fmt.Sprintf("https://%s?agentName=%s", r.ResourceProxyAddress, agentName)
	cluster := clientcmdv1.Cluster{
		Server:                   server,
		CertificateAuthorityData: r.ResourceProxyCAData,
		Extensions: []clientcmdv1.NamedExtension{
			{
				// Kept for documentation/compatibility; see the comment on
				// execExtensionKey for why argocd-agent-creds doesn't rely
				// on this one alone.
				Name:      execExtensionKey,
				Extension: runtime.RawExtension{Raw: extBytes},
			},
			{
				Name:      additionalEnvVarsExtensionKey,
				Extension: runtime.RawExtension{Raw: envBytes},
			},
		},
	}

	provider := clusterinventory.AccessProvider{
		Name:    ProviderName,
		Cluster: cluster,
	}

	// Retry on update conflicts by re-fetching the latest object each
	// attempt; concurrent writes (e.g. the label self-heal above, or the
	// ClusterProfile's own finalizer being added by another controller)
	// are expected and harmless to retry against.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest clusterinventory.ClusterProfile
		if err := r.Get(ctx, client.ObjectKeyFromObject(cp), &latest); err != nil {
			return err
		}

		found := false
		for i := range latest.Status.AccessProviders {
			if latest.Status.AccessProviders[i].Name == ProviderName {
				latest.Status.AccessProviders[i] = provider
				found = true
				break
			}
		}
		if !found {
			latest.Status.AccessProviders = append(latest.Status.AccessProviders, provider)
		}

		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               conditionAgentRegistered,
			Status:             metav1.ConditionTrue,
			Reason:             "TokenIssued",
			Message:            fmt.Sprintf("Resource-proxy token minted and AccessProvider %q published for agent %q", ProviderName, agentName),
			ObservedGeneration: latest.GetGeneration(),
		})

		return r.Status().Update(ctx, &latest)
	})
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	isOurs := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		cp, ok := obj.(*clusterinventory.ClusterProfile)
		if !ok {
			return false
		}
		return cp.Spec.ClusterManager.Name == ClusterManagerName
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterinventory.ClusterProfile{}, builder.WithPredicates(isOurs)).
		Owns(&corev1.Secret{}).
		Complete(r)
}
