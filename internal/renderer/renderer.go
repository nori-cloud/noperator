/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package renderer turns a Tenant into the set of Kubernetes manifests the
// operator must apply (namespaces, Argo CD AppProject/ApplicationSets, repo
// credentials, and an image pull secret wired into the default ServiceAccount).
//
// Rendering is pure with respect to cluster mutation: it only reads referenced
// secrets and returns objects. The controller is responsible for applying them.
package renderer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	noperatorv1alpha1 "github.com/nori-cloud/noperator/api/v1alpha1"
	"github.com/nori-cloud/noperator/internal/extensions"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	argocdAPIVersion = "argoproj.io/v1alpha1"

	// DefaultArgoCDNamespace is where AppProject, ApplicationSet, and repo
	// credential resources are written.
	DefaultArgoCDNamespace = "argocd"

	// DefaultDestinationServer is the cluster API server URL.
	DefaultDestinationServer = "https://kubernetes.default.svc"

	pullSecretName = "-pull-secret"

	// tenantLabel marks a resource as owned by a particular Tenant.
	tenantLabel = "noperator.nori-cloud.io/tenant"

	// managedByLabel identifies the tool that manages a resource.
	managedByLabel = "app.kubernetes.io/managed-by"

	// managedByValue is the value of managedByLabel for noperator-managed resources.
	managedByValue = "noperator"
)

const (
	keyAPIVersion = "apiVersion"
	keyKind       = "kind"
	keyMetadata   = "metadata"
	keyName       = "name"
	keyNamespace  = "namespace"
	keyLabels     = "labels"
	keySpec       = "spec"
	keyRepoURL    = "repoURL"
)

// tenantLabels returns the labels marking a resource as owned by a Tenant.
func tenantLabels(tenantName string) map[string]string {
	return map[string]string{
		tenantLabel:    tenantName,
		managedByLabel: managedByValue,
	}
}

// tenantLabelAny returns tenantLabels as a map[string]any for unstructured
// objects, whose deepcopy cannot handle a concrete map[string]string.
func tenantLabelAny(tenantName string) map[string]any {
	return map[string]any{
		tenantLabel:    tenantName,
		managedByLabel: managedByValue,
	}
}

// Renderer produces the manifests for a Tenant.
type Renderer struct {
	Client          client.Client
	ArgoCDNamespace string
	Registry        *extensions.Registry
}

// Render returns the ordered set of manifests for the given Tenant. Namespaces
// come first, followed by the AppProject, per-environment ApplicationSets,
// repository credential secrets, and finally the image pull secret and default
// ServiceAccount per environment.
func (r *Renderer) Render(ctx context.Context, tenant *noperatorv1alpha1.Tenant) ([]client.Object, error) {
	server := tenant.Spec.Destination.Server
	if server == "" {
		server = DefaultDestinationServer
	}

	allowlist, err := r.Registry.Resolve(tenant.Spec.Extensions)
	if err != nil {
		return nil, err
	}
	allowlist = append(allowlist, tenant.Spec.ExtraNamespaceResourceWhitelist...)
	allowlist = dedupe(allowlist)

	objs := make([]client.Object, 0)

	for _, env := range tenant.Spec.Environments {
		objs = append(objs, r.buildNamespace(tenant, env))
	}

	objs = append(objs, r.buildAppProject(tenant, allowlist, server))

	for _, env := range tenant.Spec.Environments {
		objs = append(objs, r.buildAppSet(tenant, env, server))
	}

	for i, repo := range tenant.Spec.Git {
		if repo.Credentials == nil {
			continue
		}
		secret, err := r.buildRepoSecret(ctx, tenant, repo, i)
		if err != nil {
			return nil, err
		}
		objs = append(objs, secret)
	}

	if len(tenant.Spec.ImagePullSecrets) > 0 {
		for _, env := range tenant.Spec.Environments {
			secret, err := r.buildPullSecret(ctx, tenant, env)
			if err != nil {
				return nil, err
			}
			objs = append(objs, secret, r.buildDefaultSA(tenant, env))
		}
	}

	return objs, nil
}

func (r *Renderer) buildNamespace(tenant *noperatorv1alpha1.Tenant, env string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		keyAPIVersion: "v1",
		keyKind:       "Namespace",
		keyMetadata: map[string]any{
			keyName:   NamespaceName(tenant.Name, env),
			keyLabels: tenantLabelAny(tenant.Name),
		},
	}}
}

func (r *Renderer) buildAppProject(tenant *noperatorv1alpha1.Tenant, allowlist []noperatorv1alpha1.ResourceRef, server string) *unstructured.Unstructured {
	sourceRepos := make([]any, 0, len(tenant.Spec.Git))
	for _, repo := range tenant.Spec.Git {
		sourceRepos = append(sourceRepos, repo.RepoURL)
	}

	destinations := make([]any, 0, len(tenant.Spec.Environments))
	for _, env := range tenant.Spec.Environments {
		destinations = append(destinations, map[string]any{
			keyNamespace: NamespaceName(tenant.Name, env),
			"server":     server,
		})
	}

	whitelist := make([]any, 0, len(allowlist))
	for _, ref := range allowlist {
		whitelist = append(whitelist, map[string]any{
			"group": ref.Group,
			"kind":  ref.Kind,
		})
	}

	return &unstructured.Unstructured{Object: map[string]any{
		keyAPIVersion: argocdAPIVersion,
		keyKind:       "AppProject",
		keyMetadata: map[string]any{
			keyName:      tenant.Name,
			keyNamespace: r.ArgoCDNamespace,
			keyLabels:    tenantLabelAny(tenant.Name),
		},
		keySpec: map[string]any{
			"description":                fmt.Sprintf("%s tenant", tenant.Name),
			"sourceRepos":                sourceRepos,
			"destinations":               destinations,
			"namespaceResourceWhitelist": whitelist,
			"orphanedResources":          map[string]any{"warn": true},
		},
	}}
}

func (r *Renderer) buildAppSet(tenant *noperatorv1alpha1.Tenant, env, server string) *unstructured.Unstructured {
	elements := make([]any, 0, len(tenant.Spec.Git))
	for _, repo := range tenant.Spec.Git {
		elements = append(elements, map[string]any{
			keyRepoURL: repo.RepoURL,
			"revision": repo.Revision,
		})
	}

	return &unstructured.Unstructured{Object: map[string]any{
		keyAPIVersion: argocdAPIVersion,
		keyKind:       "ApplicationSet",
		keyMetadata: map[string]any{
			keyName:      fmt.Sprintf("%s-%s", tenant.Name, env),
			keyNamespace: r.ArgoCDNamespace,
			keyLabels:    tenantLabelAny(tenant.Name),
		},
		keySpec: map[string]any{
			"generators": []any{
				map[string]any{
					"matrix": map[string]any{
						"generators": []any{
							map[string]any{
								"list": map[string]any{"elements": elements},
							},
							map[string]any{
								"git": map[string]any{
									keyRepoURL: "{{repoURL}}",
									"revision": "{{revision}}",
									"directories": []any{
										map[string]any{"path": fmt.Sprintf("*/overlays/%s", env)},
									},
								},
							},
						},
					},
				},
			},
			"template": map[string]any{
				keyMetadata: map[string]any{
					keyName: fmt.Sprintf("{{path[0]}}-%s-%s", env, tenant.Name),
				},
				keySpec: map[string]any{
					"project": tenant.Name,
					"source": map[string]any{
						keyRepoURL:       "{{repoURL}}",
						"targetRevision": "{{revision}}",
						"path":           "{{path}}",
					},
					"destination": map[string]any{
						"server":     server,
						keyNamespace: NamespaceName(tenant.Name, env),
					},
					"syncPolicy": map[string]any{
						"automated":   map[string]any{"prune": true, "selfHeal": true},
						"syncOptions": []any{"ServerSideApply=true"},
					},
				},
			},
		},
	}}
}

func (r *Renderer) buildRepoSecret(ctx context.Context, tenant *noperatorv1alpha1.Tenant, repo noperatorv1alpha1.GitRepo, idx int) (*corev1.Secret, error) {
	creds := repo.Credentials
	data := map[string][]byte{
		"type": []byte("git"),
		"url":  []byte(repo.RepoURL),
	}

	switch creds.Type {
	case noperatorv1alpha1.CredentialTypeGithubApp:
		id, err := r.resolveValue(ctx, tenant.Namespace, creds.GithubAppId)
		if err != nil {
			return nil, err
		}
		key, err := r.resolveValue(ctx, tenant.Namespace, creds.GithubAppPrivateKey)
		if err != nil {
			return nil, err
		}
		data["githubAppID"] = []byte(id)
		data["githubAppPrivateKey"] = []byte(key)

	case noperatorv1alpha1.CredentialTypePAT:
		user, err := r.resolveValue(ctx, tenant.Namespace, creds.Username)
		if err != nil {
			return nil, err
		}
		pass, err := r.resolveValue(ctx, tenant.Namespace, creds.Password)
		if err != nil {
			return nil, err
		}
		data["username"] = []byte(user)
		data["password"] = []byte(pass)

	default:
		return nil, fmt.Errorf("unknown credential type %q", creds.Type)
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-repo-%d", tenant.Name, idx),
			Namespace: r.ArgoCDNamespace,
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "repository",
				tenantLabel:                      tenant.Name,
				managedByLabel:                   managedByValue,
			},
		},
		Data: data,
	}, nil
}

func (r *Renderer) buildPullSecret(ctx context.Context, tenant *noperatorv1alpha1.Tenant, env string) (*corev1.Secret, error) {
	auths := map[string]any{}
	for _, ips := range tenant.Spec.ImagePullSecrets {
		user, err := r.resolveValue(ctx, tenant.Namespace, ips.Username)
		if err != nil {
			return nil, err
		}
		pass, err := r.resolveValue(ctx, tenant.Namespace, ips.Password)
		if err != nil {
			return nil, err
		}
		auths[ips.Registry] = map[string]any{
			"username": user,
			"password": pass,
			"auth":     base64.StdEncoding.EncodeToString([]byte(user + ":" + pass)),
		}
	}

	dockerConfig, err := json.Marshal(map[string]any{"auths": auths})
	if err != nil {
		return nil, fmt.Errorf("encode dockerconfigjson: %w", err)
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      tenant.Name + pullSecretName,
			Namespace: NamespaceName(tenant.Name, env),
			Labels:    tenantLabels(tenant.Name),
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: dockerConfig},
	}, nil
}

func (r *Renderer) buildDefaultSA(tenant *noperatorv1alpha1.Tenant, env string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: NamespaceName(tenant.Name, env),
			Labels:    tenantLabels(tenant.Name),
		},
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: tenant.Name + pullSecretName}},
	}
}

// resolveValue resolves a $secretName:key reference against a Secret in the
// given namespace, or returns the value unchanged when it is not a reference.
func (r *Renderer) resolveValue(ctx context.Context, namespace, value string) (string, error) {
	if !strings.HasPrefix(value, "$") {
		return value, nil
	}

	ref := strings.TrimPrefix(value, "$")
	name, key, ok := strings.Cut(ref, ":")
	if !ok || name == "" || key == "" {
		return "", fmt.Errorf("invalid secret reference %q, expected $name:key", value)
	}

	secret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return "", fmt.Errorf("resolve secret reference %q: %w", value, err)
	}

	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s is missing key %q", namespace, name, key)
	}
	return string(val), nil
}

// NamespaceName returns the namespace name for a tenant environment.
func NamespaceName(tenant, env string) string {
	return fmt.Sprintf("%s-%s", tenant, env)
}

func dedupe(refs []noperatorv1alpha1.ResourceRef) []noperatorv1alpha1.ResourceRef {
	seen := map[string]struct{}{}
	out := make([]noperatorv1alpha1.ResourceRef, 0, len(refs))
	for _, ref := range refs {
		key := ref.Group + "/" + ref.Kind
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}
