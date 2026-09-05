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

package extensions

import (
	"context"
	"testing"

	noperatorv1alpha1 "github.com/nori-cloud/noperator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testRegistryYAML = `
core:
  - group: ""
    kind: ServiceAccount
  - group: apps
    kind: Deployment
cnpg:
  - group: postgresql.cnpg.io
    kind: Cluster
  - group: apps
    kind: Deployment
certManager:
  - group: cert-manager.io
    kind: Issuer
`

const registryNamespace = "noperator-system"

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(noperatorv1alpha1.AddToScheme(s))
	return s
}

func newRegistryCM(dataKey, data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: DefaultConfigMapName, Namespace: registryNamespace},
		Data:       map[string]string{dataKey: data},
	}
}

func load(t *testing.T, objs ...runtime.Object) (*Registry, error) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithRuntimeObjects(objs...).Build()
	return Load(context.Background(), c, registryNamespace, DefaultConfigMapName)
}

func TestLoad(t *testing.T) {
	r, err := load(t, newRegistryCM(DataKey, testRegistryYAML))
	if err != nil {
		t.Fatalf("expected load to succeed, got %v", err)
	}
	if !r.Has("cnpg") {
		t.Fatal("expected cnpg extension to be present")
	}
}

func TestLoadMissingConfigMap(t *testing.T) {
	_, err := load(t)
	if err == nil {
		t.Fatal("expected error when ConfigMap is missing")
	}
}

func TestLoadMissingDataKey(t *testing.T) {
	_, err := load(t, newRegistryCM("other.yaml", testRegistryYAML))
	if err == nil {
		t.Fatal("expected error when data key is missing")
	}
}

func TestLoadMissingCore(t *testing.T) {
	_, err := load(t, newRegistryCM(DataKey, "cnpg:\n  - { group: postgresql.cnpg.io, kind: Cluster }\n"))
	if err == nil {
		t.Fatal("expected error when core entry is missing")
	}
}

func TestResolve(t *testing.T) {
	r, err := load(t, newRegistryCM(DataKey, testRegistryYAML))
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	got, err := r.Resolve([]noperatorv1alpha1.ExtensionRef{{Name: "cnpg"}})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	// core (2) + cnpg (2) deduped (Deployment overlaps) = 3
	if len(got) != 3 {
		t.Fatalf("expected 3 resources after dedup, got %d: %v", len(got), got)
	}
}

func TestResolveUnknownExtension(t *testing.T) {
	r, err := load(t, newRegistryCM(DataKey, testRegistryYAML))
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	_, err = r.Resolve([]noperatorv1alpha1.ExtensionRef{{Name: "does-not-exist"}})
	if err == nil {
		t.Fatal("expected error for unknown extension")
	}
	if _, ok := err.(*UnknownExtensionError); !ok {
		t.Fatalf("expected UnknownExtensionError, got %T", err)
	}
}
