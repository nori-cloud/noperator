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

package controller

import (
	"context"
	"testing"
	"time"

	noperatorv1alpha1 "github.com/nori-cloud/noperator/api/v1alpha1"
	"github.com/nori-cloud/noperator/internal/extensions"
	"github.com/nori-cloud/noperator/internal/renderer"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const registryYAML = `
core:
  - group: ""
    kind: ServiceAccount
  - group: apps
    kind: Deployment
cnpg:
  - group: postgresql.cnpg.io
    kind: Cluster
`

const (
	testNamespace = "noperator"
	envDev        = "dev"
	envProd       = "prod"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(noperatorv1alpha1.AddToScheme(s))
	return s
}

type fixture struct {
	client     client.Client
	reconciler *TenantReconciler
	tenant     *noperatorv1alpha1.Tenant
	request    reconcile.Request
}

func setupFixture(t *testing.T, mutate func(*noperatorv1alpha1.Tenant), interceptors ...interceptor.Funcs) *fixture {
	t.Helper()

	tenant := &noperatorv1alpha1.Tenant{
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
			Environments: []string{envDev, envProd},
			Extensions:   []noperatorv1alpha1.ExtensionRef{{Name: "cnpg"}},
		},
	}
	if mutate != nil {
		mutate(tenant)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: extensions.DefaultConfigMapName, Namespace: "noperator-system"},
		Data:       map[string]string{extensions.DataKey: registryYAML},
	}
	ghApp := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "norriswu0-github-app", Namespace: testNamespace},
		Data:       map[string][]byte{"privateKey": []byte("PRIVATE_KEY")},
	}

	builder := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithStatusSubresource(&noperatorv1alpha1.Tenant{}).
		WithObjects(tenant, cm, ghApp)
	if len(interceptors) > 0 {
		builder = builder.WithInterceptorFuncs(interceptors[0])
	}
	c := builder.Build()

	registry, err := extensions.Load(context.Background(), c, "noperator-system", extensions.DefaultConfigMapName)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	r := &TenantReconciler{
		Client:          c,
		Scheme:          testScheme(),
		Registry:        registry,
		ArgoCDNamespace: renderer.DefaultArgoCDNamespace,
	}

	return &fixture{
		client:     c,
		reconciler: r,
		tenant:     tenant,
		request: reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "norriswu0", Namespace: testNamespace},
		},
	}
}

func mustReconcile(t *testing.T, f *fixture) {
	t.Helper()
	if _, err := f.reconciler.Reconcile(context.Background(), f.request); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func getArgocd(t *testing.T, c client.Client, kind, name string) (*unstructured.Unstructured, error) {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: argocdGroup, Version: argocdVersion, Kind: kind})
	return u, c.Get(context.Background(), client.ObjectKey{Namespace: renderer.DefaultArgoCDNamespace, Name: name}, u)
}

func TestReconcileCreatesResources(t *testing.T) {
	f := setupFixture(t, nil)
	mustReconcile(t, f)

	// namespaces
	for _, env := range []string{envDev, envProd} {
		ns := &corev1.Namespace{}
		if err := f.client.Get(context.Background(), client.ObjectKey{Name: "norriswu0-" + env}, ns); err != nil {
			t.Fatalf("expected namespace norriswu0-%s: %v", env, err)
		}
	}

	// AppProject
	appProject, err := getArgocd(t, f.client, "AppProject", "norriswu0")
	if err != nil {
		t.Fatalf("expected AppProject: %v", err)
	}
	whitelist, _, _ := unstructured.NestedSlice(appProject.Object, "spec", "namespaceResourceWhitelist")
	if len(whitelist) != 3 { // core(2) + cnpg(1)
		t.Fatalf("expected 3 whitelist entries, got %d", len(whitelist))
	}

	// AppSets
	for _, env := range []string{envDev, envProd} {
		if _, err := getArgocd(t, f.client, "ApplicationSet", "norriswu0-"+env); err != nil {
			t.Fatalf("expected ApplicationSet norriswu0-%s: %v", env, err)
		}
	}

	// repo secret
	repoSecret := &corev1.Secret{}
	if err := f.client.Get(context.Background(), client.ObjectKey{Namespace: renderer.DefaultArgoCDNamespace, Name: "norriswu0-repo-0"}, repoSecret); err != nil {
		t.Fatalf("expected repo secret: %v", err)
	}

	// status ready
	got := &noperatorv1alpha1.Tenant{}
	if err := f.client.Get(context.Background(), f.request.NamespacedName, got); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Fatalf("expected Ready=True, got %+v", got.Status.Conditions)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	f := setupFixture(t, nil)
	mustReconcile(t, f)
	mustReconcile(t, f)

	appSets := &unstructured.UnstructuredList{}
	appSets.SetGroupVersionKind(schema.GroupVersionKind{Group: argocdGroup, Version: argocdVersion, Kind: "ApplicationSetList"})
	if err := f.client.List(context.Background(), appSets, client.InNamespace(renderer.DefaultArgoCDNamespace)); err != nil {
		t.Fatalf("list applicationsets: %v", err)
	}
	if len(appSets.Items) != 2 {
		t.Fatalf("expected 2 ApplicationSets after two reconciles, got %d", len(appSets.Items))
	}
}

func TestReconcileUnknownExtension(t *testing.T) {
	f := setupFixture(t, func(tenant *noperatorv1alpha1.Tenant) {
		tenant.Spec.Extensions = append(tenant.Spec.Extensions, noperatorv1alpha1.ExtensionRef{Name: "nope"})
	})

	if _, err := f.reconciler.Reconcile(context.Background(), f.request); err == nil {
		t.Fatal("expected error for unknown extension")
	}

	got := &noperatorv1alpha1.Tenant{}
	if err := f.client.Get(context.Background(), f.request.NamespacedName, got); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != ReasonUnknownExtension {
		t.Fatalf("expected UnknownExtension reason, got %+v", got.Status.Conditions)
	}
}

func TestFinalizerDeletion(t *testing.T) {
	f := setupFixture(t, nil)
	mustReconcile(t, f)

	// simulate kubectl delete
	latest := &noperatorv1alpha1.Tenant{}
	if err := f.client.Get(context.Background(), f.request.NamespacedName, latest); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if err := f.client.Delete(context.Background(), latest); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}

	mustReconcile(t, f)

	// AppProject should be gone
	if _, err := getArgocd(t, f.client, "AppProject", "norriswu0"); !apierrors.IsNotFound(err) {
		t.Fatalf("expected AppProject to be deleted, got %v", err)
	}

	// namespace should be marked for deletion
	ns := &corev1.Namespace{}
	if err := f.client.Get(context.Background(), client.ObjectKey{Name: "norriswu0-" + envDev}, ns); err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("expected namespace deleted or deleting, got %v", err)
	}
}

func TestFinalizerBlocksUntilChildrenGone(t *testing.T) {
	block := true
	f := setupFixture(t, nil, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if block {
				if _, ok := obj.(*corev1.Namespace); ok {
					return nil // namespaces never finish deleting
				}
			}
			return c.Delete(ctx, obj, opts...)
		},
	})
	f.reconciler.FinalizeRetryInterval = time.Second

	mustReconcile(t, f)

	// simulate kubectl delete
	latest := &noperatorv1alpha1.Tenant{}
	if err := f.client.Get(context.Background(), f.request.NamespacedName, latest); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if err := f.client.Delete(context.Background(), latest); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}

	mustReconcile(t, f)

	// finalizer must be retained while namespaces are still present
	got := &noperatorv1alpha1.Tenant{}
	if err := f.client.Get(context.Background(), f.request.NamespacedName, got); err != nil {
		t.Fatalf("get tenant after blocked finalize: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, tenantFinalizer) {
		t.Fatal("expected finalizer to be retained while namespaces are deleting")
	}
	if err := f.client.Get(context.Background(), client.ObjectKey{Name: "norriswu0-" + envDev}, &corev1.Namespace{}); err != nil {
		t.Fatalf("expected namespace still present: %v", err)
	}

	// unblock namespace deletion and reconcile again
	block = false
	mustReconcile(t, f)

	if err := f.client.Get(context.Background(), f.request.NamespacedName, &noperatorv1alpha1.Tenant{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected tenant deleted after cleanup, got %v", err)
	}
}

func TestPreserveResourcesOnDeletion(t *testing.T) {
	f := setupFixture(t, func(tenant *noperatorv1alpha1.Tenant) {
		tenant.Spec.PreserveResourcesOnDeletion = true
	})

	mustReconcile(t, f)

	// simulate kubectl delete
	latest := &noperatorv1alpha1.Tenant{}
	if err := f.client.Get(context.Background(), f.request.NamespacedName, latest); err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if err := f.client.Delete(context.Background(), latest); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}

	mustReconcile(t, f)

	// children must be preserved
	if _, err := getArgocd(t, f.client, "AppProject", "norriswu0"); err != nil {
		t.Fatalf("expected AppProject preserved: %v", err)
	}
	ns := &corev1.Namespace{}
	if err := f.client.Get(context.Background(), client.ObjectKey{Name: "norriswu0-" + envDev}, ns); err != nil {
		t.Fatalf("expected namespace preserved: %v", err)
	}

	// tenant must be gone (finalizer removed)
	if err := f.client.Get(context.Background(), f.request.NamespacedName, &noperatorv1alpha1.Tenant{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected tenant deleted after preserve, got %v", err)
	}
}
