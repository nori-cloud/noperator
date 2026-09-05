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

package renderer

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	noperatorv1alpha1 "github.com/nori-cloud/noperator/api/v1alpha1"
	"github.com/nori-cloud/noperator/internal/extensions"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

var update = flag.Bool("update", false, "update golden files")

const registryYAML = `
core:
  - group: ""
    kind: ServiceAccount
  - group: ""
    kind: Secret
  - group: apps
    kind: Deployment
cnpg:
  - group: postgresql.cnpg.io
    kind: Cluster
certManager:
  - group: cert-manager.io
    kind: Issuer
`

const testNamespace = "noperator"

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(noperatorv1alpha1.AddToScheme(s))
	return s
}

func newRenderer(t *testing.T) *Renderer {
	t.Helper()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: extensions.DefaultConfigMapName, Namespace: "noperator-system"},
		Data:       map[string]string{extensions.DataKey: registryYAML},
	}
	ghApp := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "norriswu0-github-app", Namespace: testNamespace},
		Data:       map[string][]byte{"privateKey": []byte("PRIVATE_KEY")},
	}
	ghcr := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ghcr-cred", Namespace: testNamespace},
		Data:       map[string][]byte{"username": []byte("user"), "password": []byte("pass")},
	}

	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(cm, ghApp, ghcr).Build()

	registry, err := extensions.Load(context.Background(), c, "noperator-system", extensions.DefaultConfigMapName)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	return &Renderer{Client: c, ArgoCDNamespace: DefaultArgoCDNamespace, Registry: registry}
}

func testTenant() *noperatorv1alpha1.Tenant {
	return &noperatorv1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: "norriswu0", Namespace: testNamespace},
		Spec: noperatorv1alpha1.TenantSpec{
			Git: []noperatorv1alpha1.GitRepo{
				{
					RepoURL:  "https://github.com/norriswu0/apps.git",
					Revision: "main",
					Credentials: &noperatorv1alpha1.GitCredentials{
						Type:                noperatorv1alpha1.CredentialTypeGithubApp,
						GithubAppId:         "12345",
						GithubAppPrivateKey: "$norriswu0-github-app:privateKey",
					},
				},
			},
			Environments: []string{"dev", "prod"},
			Extensions: []noperatorv1alpha1.ExtensionRef{
				{Name: "cnpg"},
				{Name: "certManager"},
			},
			ImagePullSecrets: []noperatorv1alpha1.ImagePullSecret{
				{Registry: "ghcr.io", Username: "$ghcr-cred:username", Password: "$ghcr-cred:password"},
			},
			ExtraNamespaceResourceWhitelist: []noperatorv1alpha1.ResourceRef{
				{Group: "example.com", Kind: "Foo"},
			},
		},
	}
}

func marshalManifests(objs []client.Object) (string, error) {
	parts := make([]string, 0, len(objs))
	for _, obj := range objs {
		out, err := yaml.Marshal(obj)
		if err != nil {
			return "", err
		}
		parts = append(parts, string(out))
	}
	return strings.Join(parts, "---\n"), nil
}

func TestRenderGolden(t *testing.T) {
	r := newRenderer(t)
	objs, err := r.Render(context.Background(), testTenant())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	got, err := marshalManifests(objs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	golden := filepath.Join("testdata", "render", "golden.yaml")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to generate): %v", err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch (run with -update to regenerate)\n--- got ---\n%s\n--- want ---\n%s", got, string(want))
	}
}

func TestRenderUnknownExtension(t *testing.T) {
	r := newRenderer(t)
	tenant := testTenant()
	tenant.Spec.Extensions = append(tenant.Spec.Extensions, noperatorv1alpha1.ExtensionRef{Name: "nope"})

	_, err := r.Render(context.Background(), tenant)
	if err == nil {
		t.Fatal("expected error for unknown extension")
	}
	if _, ok := err.(*extensions.UnknownExtensionError); !ok {
		t.Fatalf("expected UnknownExtensionError, got %T", err)
	}
}

func TestRenderMissingSecretRef(t *testing.T) {
	r := newRenderer(t)
	tenant := testTenant()
	tenant.Spec.Git[0].Credentials.GithubAppPrivateKey = "$does-not-exist:key"

	_, err := r.Render(context.Background(), tenant)
	if err == nil {
		t.Fatal("expected error for missing secret reference")
	}
}
