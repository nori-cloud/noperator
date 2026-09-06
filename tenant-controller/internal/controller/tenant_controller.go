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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
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

	// EventReasonCreated is emitted when a child resource is first created.
	EventReasonCreated = "Created"

	// EventReasonSynced is emitted when a child resource is reconciled to
	// its desired state (whether updated or already up to date).
	EventReasonSynced = "Synced"

	// EventReasonDeleted is emitted when a child resource is deleted during
	// tenant finalization.
	EventReasonDeleted = "Deleted"

	// EventReasonFinalizing is emitted while tenant finalization is still
	// waiting for child resources to be deleted.
	EventReasonFinalizing = "Finalizing"

	// EventReasonPreserved is emitted when tenant deletion skips cleanup because
	// preserveResourcesOnDeletion is enabled.
	EventReasonPreserved = "Preserved"
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

	// Recorder emits Kubernetes Events describing the resources the controller
	// creates, syncs, and deletes for a tenant.
	Recorder events.EventRecorder

	// RequeueInterval is how often to re-reconcile a tenant to keep its
	// resources in sync and emit heartbeat "Synced" events.
	RequeueInterval time.Duration

	// FinalizeRetryInterval is how often to retry tenant finalization while
	// child resources are still pending deletion.
	FinalizeRetryInterval time.Duration
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
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

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

		if tenant.Spec.PreserveResourcesOnDeletion {
			log.Info("Preserving tenant resources on deletion", "tenant", tenant.Name)
			r.emitEvent(tenant, EventReasonPreserved, "Preserved resources on deletion")
			controllerutil.RemoveFinalizer(tenant, tenantFinalizer)
			return ctrl.Result{}, r.Update(ctx, tenant)
		}

		log.Info("Deleting tenant resources", "tenant", tenant.Name)
		done, err := r.finalize(ctx, tenant)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !done {
			return ctrl.Result{RequeueAfter: r.FinalizeRetryInterval}, nil
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
		created, err := r.apply(ctx, obj)
		if err != nil {
			log.Error(err, "Failed to apply resource", "kind", obj.GetObjectKind().GroupVersionKind().Kind, "name", obj.GetName())
			r.setStatus(ctx, tenant, metav1.ConditionFalse, ReasonReconcileError, err.Error())
			return ctrl.Result{}, err
		}

		kind := obj.GetObjectKind().GroupVersionKind().Kind
		name := obj.GetName()
		if created {
			log.Info("Created resource", "kind", kind, "name", name, "tenant", tenant.Name)
			r.emitEvent(tenant, EventReasonCreated, "Created %s %s", kind, name)
		} else {
			log.Info("Synced resource", "kind", kind, "name", name, "tenant", tenant.Name)
			r.emitEvent(tenant, EventReasonSynced, "Synced %s %s", kind, name)
		}
	}

	r.setStatus(ctx, tenant, metav1.ConditionTrue, ReasonReconciled, "Reconciled")
	return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
}

func (r *TenantReconciler) renderer() *renderer.Renderer {
	return &renderer.Renderer{
		Client:          r.Client,
		ArgoCDNamespace: r.ArgoCDNamespace,
		Registry:        r.Registry,
	}
}

// apply creates the object if absent, updates it otherwise, and leaves
// namespaces untouched once created (they are effectively immutable). It
// returns true when the object was newly created.
func (r *TenantReconciler) apply(ctx context.Context, obj client.Object) (bool, error) {
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if apierrors.IsNotFound(err) {
		return true, r.Create(ctx, obj)
	}
	if err != nil {
		return false, err
	}
	if obj.GetObjectKind().GroupVersionKind().Kind == "Namespace" {
		return false, nil
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	return false, r.Update(ctx, obj)
}

// emitEvent records an event on the tenant for an activity the controller
// performed. Recording failures are non-fatal and only logged.
func (r *TenantReconciler) emitEvent(tenant *noperatorv1alpha1.Tenant, reason, messageFormat string, args ...any) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(tenant, nil, corev1.EventTypeNormal, reason, "", messageFormat, args...)
}

// finalize removes the Argo CD resources and tenant namespaces. The namespaces
// sit in Terminating until Argo CD prunes the workloads inside them. It returns
// done=false while any child resource is still pending deletion, so the caller
// keeps the finalizer and retries.
func (r *TenantReconciler) finalize(ctx context.Context, tenant *noperatorv1alpha1.Tenant) (bool, error) {
	log := logf.FromContext(ctx)

	// Delete ApplicationSets first so Argo CD prunes the workloads before the
	// AppProject and namespaces are torn down.
	for _, env := range tenant.Spec.Environments {
		name := fmt.Sprintf("%s-%s", tenant.Name, env)
		if err := r.deleteResource(ctx, tenant, newArgocdObject("ApplicationSet", name)); err != nil {
			return false, err
		}
	}

	if err := r.deleteResource(ctx, tenant, newArgocdObject("AppProject", tenant.Name)); err != nil {
		return false, err
	}

	for i, repo := range tenant.Spec.Git {
		if repo.Credentials == nil {
			continue
		}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-repo-%d", tenant.Name, i),
			Namespace: r.ArgoCDNamespace,
		}}
		if err := r.deleteResource(ctx, tenant, secret); err != nil {
			return false, err
		}
	}

	for _, env := range tenant.Spec.Environments {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: renderer.NamespaceName(tenant.Name, env)}}
		if err := r.deleteResource(ctx, tenant, ns); err != nil {
			return false, err
		}
	}

	remaining, err := r.remainingResources(ctx, tenant)
	if err != nil {
		return false, err
	}
	if len(remaining) > 0 {
		log.Info("Waiting for tenant resources to be deleted", "tenant", tenant.Name, "remaining", remaining)
		r.emitEvent(tenant, EventReasonFinalizing, "Waiting for %s", strings.Join(remaining, ", "))
		return false, nil
	}

	log.Info("Deleted tenant resources", "tenant", tenant.Name)
	return true, nil
}

// remainingResources returns the "kind/name" of child resources still present
// in the cluster, in deletion order. An empty slice means cleanup is complete.
func (r *TenantReconciler) remainingResources(ctx context.Context, tenant *noperatorv1alpha1.Tenant) ([]string, error) {
	remaining := make([]string, 0)

	for _, env := range tenant.Spec.Environments {
		name := fmt.Sprintf("%s-%s", tenant.Name, env)
		present, err := r.exists(ctx, newArgocdObject("ApplicationSet", name))
		if err != nil {
			return nil, err
		}
		if present {
			remaining = append(remaining, fmt.Sprintf("ApplicationSet/%s", name))
		}
	}

	present, err := r.exists(ctx, newArgocdObject("AppProject", tenant.Name))
	if err != nil {
		return nil, err
	}
	if present {
		remaining = append(remaining, fmt.Sprintf("AppProject/%s", tenant.Name))
	}

	for i, repo := range tenant.Spec.Git {
		if repo.Credentials == nil {
			continue
		}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-repo-%d", tenant.Name, i),
			Namespace: r.ArgoCDNamespace,
		}}
		present, err := r.exists(ctx, secret)
		if err != nil {
			return nil, err
		}
		if present {
			remaining = append(remaining, fmt.Sprintf("Secret/%s", secret.Name))
		}
	}

	for _, env := range tenant.Spec.Environments {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: renderer.NamespaceName(tenant.Name, env)}}
		present, err := r.exists(ctx, ns)
		if err != nil {
			return nil, err
		}
		if present {
			remaining = append(remaining, fmt.Sprintf("Namespace/%s", ns.Name))
		}
	}

	return remaining, nil
}

// exists reports whether the object is still present in the cluster.
func (r *TenantReconciler) exists(ctx context.Context, obj client.Object) (bool, error) {
	if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// deleteResource deletes the given object and records a "Deleted" event and log
// entry when it was actually removed. Already-absent resources are ignored.
func (r *TenantReconciler) deleteResource(ctx context.Context, tenant *noperatorv1alpha1.Tenant, obj client.Object) error {
	if err := r.Delete(ctx, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	kind := obj.GetObjectKind().GroupVersionKind().Kind
	name := obj.GetName()
	logf.FromContext(ctx).Info("Deleted resource", "kind", kind, "name", name, "tenant", tenant.Name)
	r.emitEvent(tenant, EventReasonDeleted, "Deleted %s %s", kind, name)
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
