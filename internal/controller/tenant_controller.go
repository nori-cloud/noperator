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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	noperatorv1alpha1 "github.com/nori-cloud/noperator/api/v1alpha1"
	"github.com/nori-cloud/noperator/internal/extensions"
	"github.com/nori-cloud/noperator/internal/renderer"
)

const (
	tenantFinalizer = "noperator.nori-cloud.io/finalizer"

	argocdGroup   = "argoproj.io"
	argocdVersion = "v1alpha1"

	// ConditionReady reflects whether the tenant is fully reconciled.
	ConditionReady = "Ready"

	// ReasonReconciled indicates the tenant reached its desired state.
	ReasonReconciled = "Reconciled"

	// ReasonReconcileError indicates a generic reconcile failure.
	ReasonReconcileError = "ReconcileError"

	// ReasonUnknownExtension indicates an extension name not in the registry.
	ReasonUnknownExtension = "UnknownExtension"
)

// TenantReconciler reconciles a Tenant object.
type TenantReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Registry is the loaded extension registry (liveness-level: loaded at
	// startup and required for reconciliation).
	Registry *extensions.Registry

	// ArgoCDNamespace is where AppProject/ApplicationSet/repo-credential
	// resources are written.
	ArgoCDNamespace string
}

// +kubebuilder:rbac:groups=noperator.nori-cloud.io,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=noperator.nori-cloud.io,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=noperator.nori-cloud.io,resources=tenants/finalizers,verbs=update
// +kubebuilder:rbac:groups=argoproj.io,resources=appprojects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=applicationsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile moves a Tenant towards its desired state by rendering and applying
// the derived Argo CD resources, and cleans them up on deletion.
func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	tenant := &noperatorv1alpha1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !tenant.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(tenant, tenantFinalizer) {
			return ctrl.Result{}, nil
		}
		log.Info("Deleting tenant resources", "tenant", tenant.Name)
		if err := r.finalize(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(tenant, tenantFinalizer)
		return ctrl.Result{}, r.Update(ctx, tenant)
	}

	if !controllerutil.ContainsFinalizer(tenant, tenantFinalizer) {
		controllerutil.AddFinalizer(tenant, tenantFinalizer)
		if err := r.Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
	}

	objects, err := r.renderer().Render(ctx, tenant)
	if err != nil {
		reason := ReasonReconcileError
		var unknown *extensions.UnknownExtensionError
		if errors.As(err, &unknown) {
			reason = ReasonUnknownExtension
		}
		r.setStatus(ctx, tenant, metav1.ConditionFalse, reason, err.Error())
		return ctrl.Result{}, err
	}

	for _, obj := range objects {
		if err := r.apply(ctx, obj); err != nil {
			log.Error(err, "Failed to apply resource", "kind", obj.GetObjectKind().GroupVersionKind().Kind, "name", obj.GetName())
			r.setStatus(ctx, tenant, metav1.ConditionFalse, ReasonReconcileError, err.Error())
			return ctrl.Result{}, err
		}
	}

	r.setStatus(ctx, tenant, metav1.ConditionTrue, ReasonReconciled, "Reconciled")
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) renderer() *renderer.Renderer {
	return &renderer.Renderer{
		Client:          r.Client,
		ArgoCDNamespace: r.ArgoCDNamespace,
		Registry:        r.Registry,
	}
}

// apply creates the object if absent, updates it otherwise, and leaves
// namespaces untouched once created (they are effectively immutable).
func (r *TenantReconciler) apply(ctx context.Context, obj client.Object) error {
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	if err != nil {
		return err
	}
	if obj.GetObjectKind().GroupVersionKind().Kind == "Namespace" {
		return nil
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, obj)
}

// finalize removes the Argo CD resources and tenant namespaces. The namespaces
// sit in Terminating until Argo CD prunes the workloads inside them.
func (r *TenantReconciler) finalize(ctx context.Context, tenant *noperatorv1alpha1.Tenant) error {
	if err := r.Delete(ctx, newArgocdObject("AppProject", tenant.Name)); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	for _, env := range tenant.Spec.Environments {
		name := fmt.Sprintf("%s-%s", tenant.Name, env)
		if err := r.Delete(ctx, newArgocdObject("ApplicationSet", name)); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	for i, repo := range tenant.Spec.Git {
		if repo.Credentials == nil {
			continue
		}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-repo-%d", tenant.Name, i),
			Namespace: r.ArgoCDNamespace,
		}}
		if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	for _, env := range tenant.Spec.Environments {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: renderer.NamespaceName(tenant.Name, env)}}
		if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func (r *TenantReconciler) setStatus(ctx context.Context, tenant *noperatorv1alpha1.Tenant, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&tenant.Status.Conditions, metav1.Condition{
		Type:               ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: tenant.Generation,
	})
	tenant.Status.ObservedGeneration = tenant.Generation
	if err := r.Status().Update(ctx, tenant); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update status", "tenant", tenant.Name)
	}
}

func newArgocdObject(kind, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: argocdGroup, Version: argocdVersion, Kind: kind})
	u.SetName(name)
	u.SetNamespace(renderer.DefaultArgoCDNamespace)
	return u
}

// SetupWithManager sets up the controller with the Manager.
func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&noperatorv1alpha1.Tenant{}).
		Named("tenant").
		Complete(r)
}
