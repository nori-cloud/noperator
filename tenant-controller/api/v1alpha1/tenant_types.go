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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// CredentialType is the kind of repository access credential.
// +kubebuilder:validation:Enum=githubApp;pat
type CredentialType string

const (
	// CredentialTypeGithubApp configures a GitHub App (ID + private key).
	CredentialTypeGithubApp CredentialType = "githubApp"

	// CredentialTypePAT configures a personal access token (username + password).
	CredentialTypePAT CredentialType = "pat"
)

// TenantSpec defines the desired state of Tenant.
type TenantSpec struct {
	// git is the list of Git repositories that make up the tenant's GitOps sources.
	// +kubebuilder:validation:MinItems=1
	Git []GitRepo `json:"git"`

	// destination defines the Argo CD destination for the tenant's applications.
	// +optional
	Destination Destination `json:"destination,omitempty"`

	// environments is the list of environment names. Each environment produces a
	// namespace {tenant}-{env} and one ApplicationSet.
	// +kubebuilder:validation:MinItems=1
	Environments []string `json:"environments"`

	// extensions is the list of extensions enabled for this tenant. Names must
	// exist in the extension registry ConfigMap.
	// +optional
	Extensions []ExtensionRef `json:"extensions,omitempty"`

	// imagePullSecrets is the list of container registries whose credentials are
	// bundled into a single dockerconfigjson secret attached to the default
	// ServiceAccount of each tenant namespace.
	// +optional
	ImagePullSecrets []ImagePullSecret `json:"imagePullSecrets,omitempty"`

	// extraNamespaceResourceWhitelist is appended to the tenant's AppProject
	// namespace resource whitelist, on top of the core allowlist and enabled
	// extensions.
	// +optional
	ExtraNamespaceResourceWhitelist []ResourceRef `json:"extraNamespaceResourceWhitelist,omitempty"`
}

// GitRepo is a single Git source for a tenant.
type GitRepo struct {
	// repoURL is the Git repository URL (e.g. https://github.com/org/apps.git).
	RepoURL string `json:"repoURL"`

	// revision is the Git revision to track.
	// +optional
	// +kubebuilder:default=main
	Revision string `json:"revision,omitempty"`

	// credentials optionally configures repository access credentials.
	// +optional
	Credentials *GitCredentials `json:"credentials,omitempty"`
}

// GitCredentials configures access to a Git repository.
type GitCredentials struct {
	// type is the credential flavor.
	Type CredentialType `json:"type"`

	// githubAppId is the GitHub App ID. Accepts an inline value or a
	// $secretName:key reference.
	// +optional
	GithubAppId string `json:"githubAppId,omitempty"`

	// githubAppPrivateKey is the GitHub App private key. Accepts an inline value
	// or a $secretName:key reference.
	// +optional
	GithubAppPrivateKey string `json:"githubAppPrivateKey,omitempty"`

	// username is the repository username (PAT flavor).
	// +optional
	Username string `json:"username,omitempty"`

	// password is the repository token/password (PAT flavor). Accepts an inline
	// value or a $secretName:key reference.
	// +optional
	Password string `json:"password,omitempty"`
}

// Destination defines where Argo CD deploys a tenant's applications.
type Destination struct {
	// server is the cluster API server URL.
	// +optional
	// +kubebuilder:default="https://kubernetes.default.svc"
	Server string `json:"server,omitempty"`
}

// ExtensionRef references an extension defined in the registry ConfigMap.
type ExtensionRef struct {
	// name is the extension name as defined in the registry ConfigMap.
	Name string `json:"name"`
}

// ImagePullSecret configures credentials for a container registry.
type ImagePullSecret struct {
	// registry is the container registry host (e.g. ghcr.io).
	Registry string `json:"registry"`

	// username accepts an inline value or a $secretName:key reference.
	Username string `json:"username"`

	// password accepts an inline value or a $secretName:key reference.
	Password string `json:"password"`
}

// ResourceRef identifies a Kubernetes resource by group and kind.
type ResourceRef struct {
	// group is the API group (empty string for core resources).
	Group string `json:"group"`

	// kind is the resource kind.
	Kind string `json:"kind"`
}

// TenantStatus defines the observed state of Tenant.
type TenantStatus struct {
	// conditions represent the current state of the Tenant resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Ready": the tenant is fully reconciled
	// - "Degraded": the tenant failed to reach its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration is the generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Tenant is the Schema for the tenants API.
type Tenant struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Tenant
	// +required
	Spec TenantSpec `json:"spec"`

	// status defines the observed state of Tenant
	// +optional
	Status TenantStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TenantList contains a list of Tenant.
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Tenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Tenant{}, &TenantList{})
		return nil
	})
}
