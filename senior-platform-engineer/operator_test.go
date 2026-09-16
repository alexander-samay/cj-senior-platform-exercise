package cjpod

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func testResource() *CjPod {
	return &CjPod{
		TypeMeta: metav1.TypeMeta{APIVersion: GroupVersion.String(), Kind: "CjPod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cjpod-123",
			Namespace: "default",
			UID:       types.UID("cjpod-uid"),
		},
		Spec: CjPodSpec{Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "demo"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:  "abc",
				Image: "nginx",
			}}},
		}},
	}
}

func ownedPod(resource *CjPod, created time.Time) *corev1.Pod {
	controller := true
	blockDeletion := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              resource.Name,
			Namespace:         resource.Namespace,
			UID:               types.UID("pod-uid"),
			CreationTimestamp: metav1.NewTime(created),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         GroupVersion.String(),
				Kind:               "CjPod",
				Name:               resource.Name,
				UID:                resource.UID,
				Controller:         &controller,
				BlockOwnerDeletion: &blockDeletion,
			}},
		},
		Spec: resource.Spec.Template.Spec,
	}
}

func requestFor(resource *CjPod) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: resource.Name, Namespace: resource.Namespace}}
}

func TestReconcileCreatesPodFromTemplate(t *testing.T) {
	ctx := context.Background()
	resource := testResource()
	clock := &fakeClock{now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource).Build()
	reconciler := &CjPodReconciler{Client: client, Scheme: scheme, Clock: clock}

	result, err := reconciler.Reconcile(ctx, requestFor(resource))
	if err != nil {
		t.Fatalf("reconcile returned error: %v", err)
	}
	if result.RequeueAfter != PodLifetime {
		t.Fatalf("expected requeue after %s, got %s", PodLifetime, result.RequeueAfter)
	}

	var pod corev1.Pod
	if err := client.Get(ctx, requestFor(resource).NamespacedName, &pod); err != nil {
		t.Fatalf("created Pod not found: %v", err)
	}
	if pod.Name != resource.Name || pod.Namespace != resource.Namespace {
		t.Fatalf("unexpected identity %s/%s", pod.Namespace, pod.Name)
	}
	if pod.Spec.Containers[0].Image != "nginx" || pod.Labels["app"] != "demo" {
		t.Fatalf("Pod did not preserve template: %#v", pod)
	}
	if !metav1.IsControlledBy(&pod, resource) {
		t.Fatal("created Pod is missing the CjPod controller reference")
	}
}

func TestReconcileNeverDeletesBeforeThreeMinutes(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	resource := testResource()
	pod := ownedPod(resource, started)
	clock := &fakeClock{now: started.Add(2*time.Minute + 59*time.Second)}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource, pod).Build()
	reconciler := &CjPodReconciler{Client: client, Scheme: scheme, Clock: clock}

	result, err := reconciler.Reconcile(ctx, requestFor(resource))
	if err != nil {
		t.Fatalf("reconcile returned error: %v", err)
	}
	if result.RequeueAfter != time.Second {
		t.Fatalf("expected one second remaining, got %s", result.RequeueAfter)
	}
	var got corev1.Pod
	if err := client.Get(ctx, requestFor(resource).NamespacedName, &got); err != nil {
		t.Fatalf("Pod was deleted early: %v", err)
	}
}

func TestReconcileResumesTimerAfterRestartAndCompletes(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	resource := testResource()
	pod := ownedPod(resource, started)
	clock := &fakeClock{now: started.Add(time.Minute)}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource, pod).Build()

	firstProcess := &CjPodReconciler{Client: client, Scheme: scheme, Clock: clock}
	result, err := firstProcess.Reconcile(ctx, requestFor(resource))
	if err != nil {
		t.Fatalf("initial reconcile returned error: %v", err)
	}
	if result.RequeueAfter != 2*time.Minute {
		t.Fatalf("expected persisted timer to have two minutes left, got %s", result.RequeueAfter)
	}

	// A new reconciler simulates a process restart. It uses the Pod's persisted
	// creation timestamp rather than starting another three-minute timer.
	clock.now = started.Add(PodLifetime)
	secondProcess := &CjPodReconciler{Client: client, Scheme: scheme, Clock: clock}
	if _, err := secondProcess.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("deletion reconcile returned error: %v", err)
	}

	var gotPod corev1.Pod
	err = client.Get(ctx, requestFor(resource).NamespacedName, &gotPod)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected Pod to be deleted, got: %v", err)
	}

	if _, err := secondProcess.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("completion reconcile returned error: %v", err)
	}
	var gotResource CjPod
	if err := client.Get(ctx, requestFor(resource).NamespacedName, &gotResource); err != nil {
		t.Fatal(err)
	}
	if gotResource.Status.Phase != PhaseCompleted || gotResource.Status.CompletedAt == nil {
		t.Fatalf("expected completed status, got %#v", gotResource.Status)
	}
}

func TestReconcileRefusesToDeleteUnownedPod(t *testing.T) {
	ctx := context.Background()
	resource := testResource()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              resource.Name,
			Namespace:         resource.Namespace,
			UID:               types.UID("someone-elses-pod"),
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
		},
		Spec: resource.Spec.Template.Spec,
	}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource, pod).Build()
	reconciler := &CjPodReconciler{Client: client, Scheme: scheme, Clock: &fakeClock{now: time.Now()}}

	_, err := reconciler.Reconcile(ctx, requestFor(resource))
	if err == nil || !strings.Contains(err.Error(), "not controlled") {
		t.Fatalf("expected ownership error, got %v", err)
	}
	var got corev1.Pod
	if err := client.Get(ctx, requestFor(resource).NamespacedName, &got); err != nil {
		t.Fatalf("unowned Pod was changed or deleted: %v", err)
	}
}

func TestCompletedResourceDoesNotCreateAnotherPod(t *testing.T) {
	ctx := context.Background()
	resource := testResource()
	resource.Status.Phase = PhaseCompleted
	scheme := testScheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource).Build()
	reconciler := &CjPodReconciler{Client: client, Scheme: scheme, Clock: &fakeClock{now: time.Now()}}

	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("reconcile returned error: %v", err)
	}
	var pod corev1.Pod
	err := client.Get(ctx, requestFor(resource).NamespacedName, &pod)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("completed CjPod created another Pod: %v", err)
	}
}
