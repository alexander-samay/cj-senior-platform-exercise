package cjpod

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	PodLifetime       = 3 * time.Minute
	PhaseRunning      = "Running"
	PhaseDeleting     = "Deleting"
	PhaseCompleted    = "Completed"
	deletionPollDelay = time.Second
)

var GroupVersion = schema.GroupVersion{Group: "interview.cj.dev", Version: "v1"}

// CjPodSpec defines the desired Pod template.
type CjPodSpec struct {
	Template corev1.PodTemplateSpec `json:"template"`
}

// CjPodStatus persists the lifecycle across controller restarts.
type CjPodStatus struct {
	Phase       string       `json:"phase,omitempty"`
	PodUID      types.UID    `json:"podUID,omitempty"`
	StartedAt   *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

type CjPod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CjPodSpec   `json:"spec,omitempty"`
	Status CjPodStatus `json:"status,omitempty"`
}

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
	out.Spec.Template = *in.Spec.Template.DeepCopy()
	if in.Status.StartedAt != nil {
		startedAt := in.Status.StartedAt.DeepCopy()
		out.Status.StartedAt = startedAt
	}
	if in.Status.CompletedAt != nil {
		completedAt := in.Status.CompletedAt.DeepCopy()
		out.Status.CompletedAt = completedAt
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
	Scheme *runtime.Scheme
	Clock  Clock
}

func (r *CjPodReconciler) now() time.Time {
	if r.Clock == nil {
		return time.Now()
	}
	return r.Clock.Now()
}

func (r *CjPodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var resource CjPod
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if resource.Status.Phase == PhaseCompleted {
		return ctrl.Result{}, nil
	}

	var pod corev1.Pod
	err := r.Get(ctx, req.NamespacedName, &pod)
	if apierrors.IsNotFound(err) {
		if resource.Status.Phase == PhaseDeleting {
			return ctrl.Result{}, r.markCompleted(ctx, &resource)
		}
		return r.createPod(ctx, &resource)
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if !metav1.IsControlledBy(&pod, &resource) {
		return ctrl.Result{}, fmt.Errorf("pod %s/%s already exists and is not controlled by CjPod UID %s", pod.Namespace, pod.Name, resource.UID)
	}
	if resource.Status.PodUID != "" && resource.Status.PodUID != pod.UID {
		return ctrl.Result{}, fmt.Errorf("pod %s/%s UID changed from %s to %s; refusing to delete it", pod.Namespace, pod.Name, resource.Status.PodUID, pod.UID)
	}

	if resource.Status.Phase == PhaseDeleting {
		return r.deleteOwnedPod(ctx, &pod)
	}

	startedAt := pod.CreationTimestamp.Time
	if startedAt.IsZero() && resource.Status.StartedAt != nil {
		startedAt = resource.Status.StartedAt.Time
	}
	if startedAt.IsZero() {
		return ctrl.Result{}, errors.New("owned pod has neither a creation timestamp nor a persisted start time")
	}

	if resource.Status.Phase != PhaseRunning || resource.Status.StartedAt == nil || resource.Status.PodUID == "" {
		resource.Status.Phase = PhaseRunning
		resource.Status.PodUID = pod.UID
		resource.Status.StartedAt = &metav1.Time{Time: startedAt}
		if err := r.Status().Update(ctx, &resource); err != nil {
			return ctrl.Result{}, err
		}
	}

	remaining := PodLifetime - r.now().Sub(startedAt)
	if remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	// Persist intent before deleting. If the process exits after this update,
	// the next reconcile resumes deletion instead of creating a replacement.
	resource.Status.Phase = PhaseDeleting
	if err := r.Status().Update(ctx, &resource); err != nil {
		return ctrl.Result{}, err
	}
	return r.deleteOwnedPod(ctx, &pod)
}

func (r *CjPodReconciler) createPod(ctx context.Context, resource *CjPod) (ctrl.Result, error) {
	now := metav1.NewTime(r.now())
	pod := corev1.Pod{
		ObjectMeta: *resource.Spec.Template.ObjectMeta.DeepCopy(),
		Spec:       *resource.Spec.Template.Spec.DeepCopy(),
	}
	pod.Name = resource.Name
	pod.Namespace = resource.Namespace
	pod.GenerateName = ""

	if err := controllerutil.SetControllerReference(resource, &pod, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, &pod); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	resource.Status.Phase = PhaseRunning
	resource.Status.PodUID = pod.UID
	resource.Status.StartedAt = &now
	resource.Status.CompletedAt = nil
	if err := r.Status().Update(ctx, resource); err != nil {
		// The Pod owner reference lets a later reconcile recover its timestamp
		// and UID if the process stops before this status write succeeds.
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: PodLifetime}, nil
}

func (r *CjPodReconciler) deleteOwnedPod(ctx context.Context, pod *corev1.Pod) (ctrl.Result, error) {
	uid := pod.UID
	err := r.Delete(ctx, pod, client.Preconditions{UID: &uid})
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: deletionPollDelay}, nil
}

func (r *CjPodReconciler) markCompleted(ctx context.Context, resource *CjPod) error {
	now := metav1.NewTime(r.now())
	resource.Status.Phase = PhaseCompleted
	resource.Status.CompletedAt = &now
	return r.Status().Update(ctx, resource)
}

func (r *CjPodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = realClock{}
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&CjPod{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
