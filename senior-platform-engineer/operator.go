package cjpod

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	PodLifetime                   = 3 * time.Minute
	PhaseRunning       CjPodPhase = "Running"
	PhaseDeleting      CjPodPhase = "Deleting"
	PhaseCompleted     CjPodPhase = "Completed"
	PhaseFailed        CjPodPhase = "Failed"
	ConditionReady                = "Ready"
	ConditionCompleted            = "Completed"
	ConditionFailed               = "Failed"
	deletionPollDelay             = time.Second
)

var GroupVersion = schema.GroupVersion{Group: "interview.cj.dev", Version: "v1"}

// +kubebuilder:validation:Enum=Running;Deleting;Completed;Failed
type CjPodPhase string

// CjPodSpec defines the desired Pod template.
type CjPodSpec struct {
	Template CjPodTemplate `json:"template"`
}

// CjPodTemplate exposes only safe metadata fields and retains the full,
// generated OpenAPI schema for PodSpec.
type CjPodTemplate struct {
	Metadata CjPodTemplateMetadata `json:"metadata,omitempty"`
	Spec     corev1.PodSpec        `json:"spec"`
}

type CjPodTemplateMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// CjPodStatus persists the lifecycle across controller restarts.
type CjPodStatus struct {
	Phase              CjPodPhase         `json:"phase,omitempty"`
	PodUID             types.UID          `json:"podUID,omitempty"`
	StartedAt          *metav1.Time       `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time       `json:"completedAt,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=cjpods,scope=Namespaced
// +kubebuilder:validation:XValidation:rule="self.spec == oldSelf.spec",message="spec is immutable; create a new CjPod for another run"
// +kubebuilder:validation:XValidation:rule="self.spec.template.spec.containers.all(c, c.name.size() > 0 && has(c.image) && c.image.size() > 0)",message="every container must have a non-empty name and image"
type CjPod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CjPodSpec   `json:"spec,omitempty"`
	Status CjPodStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type CjPodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CjPod `json:"items"`
}

func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &CjPod{}, &CjPodList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

func (in *CjPod) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(CjPod)
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	out.Spec.Template.Metadata.Labels = maps.Clone(in.Spec.Template.Metadata.Labels)
	out.Spec.Template.Metadata.Annotations = maps.Clone(in.Spec.Template.Metadata.Annotations)
	out.Spec.Template.Spec = *in.Spec.Template.Spec.DeepCopy()
	if in.Status.StartedAt != nil {
		startedAt := in.Status.StartedAt.DeepCopy()
		out.Status.StartedAt = startedAt
	}
	if in.Status.CompletedAt != nil {
		completedAt := in.Status.CompletedAt.DeepCopy()
		out.Status.CompletedAt = completedAt
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
	return out
}

func (in *CjPodList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(CjPodList)
	*out = *in
	out.ListMeta = in.ListMeta
	if in.Items != nil {
		out.Items = make([]CjPod, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *(in.Items[i].DeepCopyObject().(*CjPod))
		}
	}
	return out
}

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type CjPodReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Clock    Clock
	Lifetime time.Duration
	Recorder record.EventRecorder
}

func (r *CjPodReconciler) lifetime() time.Duration {
	if r.Lifetime > 0 {
		return r.Lifetime
	}
	return PodLifetime
}

func (r *CjPodReconciler) now() time.Time {
	if r.Clock == nil {
		return time.Now()
	}
	return r.Clock.Now()
}

func (r *CjPodReconciler) event(resource *CjPod, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(resource, eventType, reason, message)
	}
}

func (r *CjPodReconciler) setCondition(resource *CjPod, conditionType string, status metav1.ConditionStatus, reason, message string) {
	apiMeta.SetStatusCondition(&resource.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: resource.Generation,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.NewTime(r.now()),
	})
	resource.Status.ObservedGeneration = resource.Generation
}

func (r *CjPodReconciler) reportFailure(ctx context.Context, resource *CjPod, reason string, reconcileErr error) (ctrl.Result, error) {
	r.setCondition(resource, ConditionReady, metav1.ConditionFalse, reason, reconcileErr.Error())
	r.setCondition(resource, ConditionCompleted, metav1.ConditionFalse, "LifecycleIncomplete", "The managed Pod lifecycle has not completed")
	r.setCondition(resource, ConditionFailed, metav1.ConditionTrue, reason, reconcileErr.Error())
	r.event(resource, corev1.EventTypeWarning, reason, reconcileErr.Error())
	// Preserve the reconciliation error even if the best-effort diagnostic
	// status write conflicts. controller-runtime will retry the operation.
	_ = r.Status().Update(ctx, resource)
	return ctrl.Result{}, reconcileErr
}

func (r *CjPodReconciler) clearFailure(ctx context.Context, resource *CjPod) error {
	failed := apiMeta.FindStatusCondition(resource.Status.Conditions, ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionTrue {
		return nil
	}
	r.setCondition(resource, ConditionFailed, metav1.ConditionFalse, "ReconcileSucceeded", "No reconciliation error is active")
	switch resource.Status.Phase {
	case PhaseDeleting:
		r.setCondition(resource, ConditionReady, metav1.ConditionFalse, "DeletionInProgress", "Pod deletion is in progress")
	default:
		r.setCondition(resource, ConditionReady, metav1.ConditionTrue, "PodRunning", "The managed Pod is running")
	}
	r.setCondition(resource, ConditionCompleted, metav1.ConditionFalse, "LifecycleIncomplete", "The managed Pod lifecycle has not completed")
	return r.Status().Update(ctx, resource)
}

func (r *CjPodReconciler) markFailed(ctx context.Context, resource *CjPod, reason, message string) error {
	resource.Status.Phase = PhaseFailed
	r.setCondition(resource, ConditionReady, metav1.ConditionFalse, reason, message)
	r.setCondition(resource, ConditionCompleted, metav1.ConditionFalse, "LifecycleIncomplete", "The managed Pod lifecycle did not complete")
	r.setCondition(resource, ConditionFailed, metav1.ConditionTrue, reason, message)
	r.event(resource, corev1.EventTypeWarning, reason, message)
	return r.Status().Update(ctx, resource)
}

func (r *CjPodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var resource CjPod
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if resource.Status.Phase == PhaseCompleted || resource.Status.Phase == PhaseFailed {
		return ctrl.Result{}, nil
	}

	var pod corev1.Pod
	err := r.Get(ctx, req.NamespacedName, &pod)
	if apierrors.IsNotFound(err) {
		if resource.Status.Phase == PhaseDeleting {
			return ctrl.Result{}, r.markCompleted(ctx, &resource)
		}
		if resource.Status.Phase == PhaseRunning && resource.Status.PodUID != "" {
			return ctrl.Result{}, r.markFailed(ctx, &resource, "PodDeletedExternally", "The managed Pod disappeared before the controller began deletion")
		}
		return r.createPod(ctx, &resource)
	}
	if err != nil {
		return r.reportFailure(ctx, &resource, "PodReadFailed", err)
	}

	if !metav1.IsControlledBy(&pod, &resource) {
		return r.reportFailure(ctx, &resource, "OwnershipConflict", fmt.Errorf("pod %s/%s already exists and is not controlled by CjPod UID %s", pod.Namespace, pod.Name, resource.UID))
	}
	if resource.Status.PodUID != "" && resource.Status.PodUID != pod.UID {
		return r.reportFailure(ctx, &resource, "PodUIDChanged", fmt.Errorf("pod %s/%s UID changed from %s to %s; refusing to delete it", pod.Namespace, pod.Name, resource.Status.PodUID, pod.UID))
	}
	if err := r.clearFailure(ctx, &resource); err != nil {
		return ctrl.Result{}, err
	}

	if resource.Status.Phase == PhaseDeleting {
		return r.deleteOwnedPod(ctx, &resource, &pod)
	}

	startedAt := pod.CreationTimestamp.Time
	if startedAt.IsZero() && resource.Status.StartedAt != nil {
		startedAt = resource.Status.StartedAt.Time
	}
	if startedAt.IsZero() {
		return r.reportFailure(ctx, &resource, "StartTimeUnavailable", errors.New("owned pod has neither a creation timestamp nor a persisted start time"))
	}

	if resource.Status.Phase != PhaseRunning || resource.Status.StartedAt == nil || resource.Status.PodUID == "" {
		resource.Status.Phase = PhaseRunning
		resource.Status.PodUID = pod.UID
		resource.Status.StartedAt = &metav1.Time{Time: startedAt}
		r.setCondition(&resource, ConditionReady, metav1.ConditionTrue, "PodRunning", "The managed Pod is running")
		r.setCondition(&resource, ConditionCompleted, metav1.ConditionFalse, "LifecycleIncomplete", "The managed Pod lifecycle has not completed")
		r.setCondition(&resource, ConditionFailed, metav1.ConditionFalse, "ReconcileSucceeded", "No reconciliation error is active")
		if err := r.Status().Update(ctx, &resource); err != nil {
			return ctrl.Result{}, err
		}
	}

	remaining := r.lifetime() - r.now().Sub(startedAt)
	if remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	// Persist intent before deleting. If the process exits after this update,
	// the next reconcile resumes deletion instead of creating a replacement.
	resource.Status.Phase = PhaseDeleting
	r.setCondition(&resource, ConditionReady, metav1.ConditionFalse, "DeletionInProgress", "Pod reached its deadline and deletion is in progress")
	r.setCondition(&resource, ConditionCompleted, metav1.ConditionFalse, "LifecycleIncomplete", "The managed Pod lifecycle has not completed")
	if err := r.Status().Update(ctx, &resource); err != nil {
		return ctrl.Result{}, err
	}
	return r.deleteOwnedPod(ctx, &resource, &pod)
}

func (r *CjPodReconciler) createPod(ctx context.Context, resource *CjPod) (ctrl.Result, error) {
	now := metav1.NewTime(r.now())
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      maps.Clone(resource.Spec.Template.Metadata.Labels),
			Annotations: maps.Clone(resource.Spec.Template.Metadata.Annotations),
		},
		Spec: *resource.Spec.Template.Spec.DeepCopy(),
	}
	pod.Name = resource.Name
	pod.Namespace = resource.Namespace
	pod.GenerateName = ""

	if err := controllerutil.SetControllerReference(resource, &pod, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	// CjPod has no finalizer, so blocking owner deletion would only require
	// unnecessary cjpods/finalizers RBAC without providing a lifecycle benefit.
	for i := range pod.OwnerReferences {
		if pod.OwnerReferences[i].UID == resource.UID && pod.OwnerReferences[i].Controller != nil && *pod.OwnerReferences[i].Controller {
			blockOwnerDeletion := false
			pod.OwnerReferences[i].BlockOwnerDeletion = &blockOwnerDeletion
		}
	}
	if err := r.Create(ctx, &pod); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return r.reportFailure(ctx, resource, "PodCreateFailed", err)
	}
	r.event(resource, corev1.EventTypeNormal, "PodCreated", "Created the managed Pod")

	resource.Status.Phase = PhaseRunning
	resource.Status.PodUID = pod.UID
	resource.Status.StartedAt = &now
	resource.Status.CompletedAt = nil
	r.setCondition(resource, ConditionReady, metav1.ConditionTrue, "PodRunning", "The managed Pod is running")
	r.setCondition(resource, ConditionCompleted, metav1.ConditionFalse, "LifecycleIncomplete", "The managed Pod lifecycle has not completed")
	r.setCondition(resource, ConditionFailed, metav1.ConditionFalse, "ReconcileSucceeded", "No reconciliation error is active")
	if err := r.Status().Update(ctx, resource); err != nil {
		// The Pod owner reference lets a later reconcile recover its timestamp
		// and UID if the process stops before this status write succeeds.
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.lifetime()}, nil
}

func (r *CjPodReconciler) deleteOwnedPod(ctx context.Context, resource *CjPod, pod *corev1.Pod) (ctrl.Result, error) {
	uid := pod.UID
	err := r.Delete(ctx, pod, client.Preconditions{UID: &uid})
	if err != nil && !apierrors.IsNotFound(err) {
		return r.reportFailure(ctx, resource, "PodDeleteFailed", err)
	}
	r.event(resource, corev1.EventTypeNormal, "PodDeletionRequested", "Requested deletion of the managed Pod")
	return ctrl.Result{RequeueAfter: deletionPollDelay}, nil
}

func (r *CjPodReconciler) markCompleted(ctx context.Context, resource *CjPod) error {
	now := metav1.NewTime(r.now())
	resource.Status.Phase = PhaseCompleted
	resource.Status.CompletedAt = &now
	r.setCondition(resource, ConditionReady, metav1.ConditionFalse, "LifecycleCompleted", "The managed Pod no longer needs to be running")
	r.setCondition(resource, ConditionCompleted, metav1.ConditionTrue, "Completed", "Managed Pod was deleted after its minimum lifetime")
	r.setCondition(resource, ConditionFailed, metav1.ConditionFalse, "ReconcileSucceeded", "No reconciliation error is active")
	r.event(resource, corev1.EventTypeNormal, "Completed", "Managed Pod deletion completed")
	return r.Status().Update(ctx, resource)
}

func (r *CjPodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = realClock{}
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("cjpod-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&CjPod{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
