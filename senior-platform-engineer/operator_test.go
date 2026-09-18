package cjpod

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type failingStatusWriter struct {
	client.SubResourceWriter
	failures int
	err      error
}

func (w *failingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if w.failures > 0 {
		w.failures--
		return w.err
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

type statusFailingClient struct {
	client.Client
	writer *failingStatusWriter
}

func newStatusFailingClient(delegate client.Client, failures int, err error) *statusFailingClient {
	return &statusFailingClient{
		Client: delegate,
		writer: &failingStatusWriter{SubResourceWriter: delegate.Status(), failures: failures, err: err},
	}
}

func (c *statusFailingClient) Status() client.SubResourceWriter { return c.writer }

type deleteFailingClient struct {
	client.Client
	failures int
	err      error
	calls    int
}

type podReadFailingClient struct {
	client.Client
	failures int
	err      error
}

type podCreateFailingClient struct {
	client.Client
	failures int
	err      error
}

func (c *podCreateFailingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, isPod := obj.(*corev1.Pod); isPod && c.failures > 0 {
		c.failures--
		return c.err
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *podReadFailingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, isPod := obj.(*corev1.Pod); isPod && c.failures > 0 {
		c.failures--
		return c.err
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *deleteFailingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.calls++
	if c.failures > 0 {
		c.failures--
		return c.err
	}
	return c.Client.Delete(ctx, obj, opts...)
}

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
		Spec: CjPodSpec{Template: CjPodTemplate{
			Metadata: CjPodTemplateMetadata{
				Labels:      map[string]string{"app": "demo"},
				Annotations: map[string]string{"example.test/trace": "enabled"},
			},
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
	if pod.Annotations["example.test/trace"] != "enabled" || len(pod.Finalizers) != 0 {
		t.Fatalf("Pod metadata was not safely rendered: %#v", pod.ObjectMeta)
	}
	if !metav1.IsControlledBy(&pod, resource) {
		t.Fatal("created Pod is missing the CjPod controller reference")
	}
	if len(pod.OwnerReferences) != 1 || pod.OwnerReferences[0].BlockOwnerDeletion == nil || *pod.OwnerReferences[0].BlockOwnerDeletion {
		t.Fatalf("expected one non-blocking controller owner reference, got %#v", pod.OwnerReferences)
	}
	var current CjPod
	if err := client.Get(ctx, requestFor(resource).NamespacedName, &current); err != nil {
		t.Fatal(err)
	}
	ready := findCondition(current.Status.Conditions, ConditionReady)
	completed := findCondition(current.Status.Conditions, ConditionCompleted)
	if ready == nil || ready.Status != metav1.ConditionTrue || completed == nil || completed.Status != metav1.ConditionFalse {
		t.Fatalf("unexpected active lifecycle conditions: %#v", current.Status.Conditions)
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
	completed := findCondition(gotResource.Status.Conditions, ConditionCompleted)
	ready := findCondition(gotResource.Status.Conditions, ConditionReady)
	if completed == nil || completed.Status != metav1.ConditionTrue || ready == nil || ready.Status != metav1.ConditionFalse {
		t.Fatalf("unexpected completed lifecycle conditions: %#v", gotResource.Status.Conditions)
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

func TestReconcileResumesDeletionAfterRestart(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	resource := testResource()
	pod := ownedPod(resource, started)
	resource.Status = CjPodStatus{
		Phase:     PhaseDeleting,
		PodUID:    pod.UID,
		StartedAt: &metav1.Time{Time: started},
	}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource, pod).Build()
	restartedProcess := &CjPodReconciler{
		Client: client,
		Scheme: scheme,
		Clock:  &fakeClock{now: started.Add(PodLifetime + time.Minute)},
	}

	if _, err := restartedProcess.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("deletion recovery returned error: %v", err)
	}
	var got corev1.Pod
	err := client.Get(ctx, requestFor(resource).NamespacedName, &got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected restarted controller to finish deletion, got %v", err)
	}
}

func TestReconcileRefusesOwnedPodWithDifferentUID(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	resource := testResource()
	pod := ownedPod(resource, started)
	resource.Status = CjPodStatus{
		Phase:     PhaseRunning,
		PodUID:    types.UID("original-pod-uid"),
		StartedAt: &metav1.Time{Time: started},
	}
	scheme := testScheme(t)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource, pod).Build()
	reconciler := &CjPodReconciler{
		Client: client,
		Scheme: scheme,
		Clock:  &fakeClock{now: started.Add(time.Hour)},
	}

	_, err := reconciler.Reconcile(ctx, requestFor(resource))
	if err == nil || !strings.Contains(err.Error(), "UID changed") {
		t.Fatalf("expected UID safety error, got %v", err)
	}
	var got corev1.Pod
	if err := client.Get(ctx, requestFor(resource).NamespacedName, &got); err != nil {
		t.Fatalf("replacement Pod was deleted: %v", err)
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

func TestExternalPodDeletionTerminatesWithoutReplacement(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	resource := testResource()
	resource.Status = CjPodStatus{
		Phase:     PhaseRunning,
		PodUID:    types.UID("deleted-pod-uid"),
		StartedAt: &metav1.Time{Time: started},
	}
	scheme := testScheme(t)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource).Build()
	reconciler := &CjPodReconciler{Client: baseClient, Scheme: scheme, Clock: &fakeClock{now: started.Add(time.Minute)}}

	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("reconcile returned error: %v", err)
	}
	var current CjPod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase != PhaseFailed {
		t.Fatalf("expected terminal failure after external deletion, got %#v", current.Status)
	}
	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("terminal failure reconcile returned error: %v", err)
	}
	var pod corev1.Pod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &pod); !apierrors.IsNotFound(err) {
		t.Fatalf("controller replaced an externally deleted Pod: %v", err)
	}
}

func TestRecoversWhenPodCreationSucceedsButStatusUpdateFails(t *testing.T) {
	ctx := context.Background()
	resource := testResource()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: started}
	scheme := testScheme(t)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource).Build()
	injectedError := errors.New("injected status write failure")
	failingClient := newStatusFailingClient(baseClient, 1, injectedError)
	reconciler := &CjPodReconciler{Client: failingClient, Scheme: scheme, Clock: clock}

	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); !errors.Is(err, injectedError) {
		t.Fatalf("expected injected status error, got %v", err)
	}
	var pod corev1.Pod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &pod); err != nil {
		t.Fatalf("Pod creation should survive the failed status write: %v", err)
	}
	// The fake API server does not assign timestamps, so model the timestamp
	// that a real API server persists before the retry.
	pod.CreationTimestamp = metav1.NewTime(started)
	pod.UID = types.UID("recovered-pod-uid")
	if err := baseClient.Update(ctx, &pod); err != nil {
		t.Fatal(err)
	}

	restarted := &CjPodReconciler{Client: baseClient, Scheme: scheme, Clock: &fakeClock{now: started.Add(time.Minute)}}
	result, err := restarted.Reconcile(ctx, requestFor(resource))
	if err != nil {
		t.Fatalf("restarted reconcile failed: %v", err)
	}
	if result.RequeueAfter != 2*time.Minute {
		t.Fatalf("expected recovered timer to have two minutes left, got %s", result.RequeueAfter)
	}
	var recovered CjPod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Status.PodUID != pod.UID || recovered.Status.StartedAt == nil {
		t.Fatalf("controller did not reconstruct status from the Pod: %#v", recovered.Status)
	}
}

func TestRetriesDeletionAfterDeleteFailure(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	resource := testResource()
	pod := ownedPod(resource, started)
	scheme := testScheme(t)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource, pod).Build()
	injectedError := errors.New("injected delete failure")
	failingClient := &deleteFailingClient{Client: baseClient, failures: 1, err: injectedError}
	reconciler := &CjPodReconciler{Client: failingClient, Scheme: scheme, Clock: &fakeClock{now: started.Add(PodLifetime)}}

	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); !errors.Is(err, injectedError) {
		t.Fatalf("expected injected delete error, got %v", err)
	}
	var persisted CjPod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status.Phase != PhaseDeleting {
		t.Fatalf("deletion intent was not persisted before delete: %#v", persisted.Status)
	}
	var stillPresent corev1.Pod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &stillPresent); err != nil {
		t.Fatalf("Pod should remain after injected delete failure: %v", err)
	}

	restarted := &CjPodReconciler{Client: baseClient, Scheme: scheme, Clock: &fakeClock{now: started.Add(PodLifetime + time.Minute)}}
	if _, err := restarted.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("delete retry failed: %v", err)
	}
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &stillPresent); !apierrors.IsNotFound(err) {
		t.Fatalf("Pod was not deleted on retry: %v", err)
	}
}

func TestRetriesCompletionAfterStatusFailure(t *testing.T) {
	ctx := context.Background()
	resource := testResource()
	resource.Status.Phase = PhaseDeleting
	scheme := testScheme(t)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource).Build()
	injectedError := apierrors.NewConflict(
		schema.GroupResource{Group: GroupVersion.Group, Resource: "cjpods"},
		resource.Name,
		errors.New("injected resource-version conflict"),
	)
	failingClient := newStatusFailingClient(baseClient, 1, injectedError)
	reconciler := &CjPodReconciler{Client: failingClient, Scheme: scheme, Clock: &fakeClock{now: time.Now()}}

	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); !apierrors.IsConflict(err) {
		t.Fatalf("expected injected status conflict, got %v", err)
	}
	restarted := &CjPodReconciler{Client: baseClient, Scheme: scheme, Clock: &fakeClock{now: time.Now()}}
	if _, err := restarted.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("completion retry failed: %v", err)
	}
	var completed CjPod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Status.Phase != PhaseCompleted || completed.Status.CompletedAt == nil {
		t.Fatalf("completion was not persisted on retry: %#v", completed.Status)
	}
}

func TestMissingParentIsIgnored(t *testing.T) {
	scheme := testScheme(t)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).Build()
	reconciler := &CjPodReconciler{Client: baseClient, Scheme: scheme, Clock: &fakeClock{now: time.Now()}}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Name: "deleted-parent", Namespace: "default"}}
	if result, err := reconciler.Reconcile(context.Background(), request); err != nil || result != (ctrl.Result{}) {
		t.Fatalf("missing parent should be ignored, got result=%#v error=%v", result, err)
	}
}

func TestRetriesAfterTemporaryPodReadFailure(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	resource := testResource()
	pod := ownedPod(resource, started)
	scheme := testScheme(t)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource, pod).Build()
	injectedError := errors.New("injected API read failure")
	failingClient := &podReadFailingClient{Client: baseClient, failures: 1, err: injectedError}
	reconciler := &CjPodReconciler{Client: failingClient, Scheme: scheme, Clock: &fakeClock{now: started.Add(time.Minute)}}

	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); !errors.Is(err, injectedError) {
		t.Fatalf("expected injected read error, got %v", err)
	}
	var diagnosed CjPod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &diagnosed); err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, condition := range diagnosed.Status.Conditions {
		if condition.Type == ConditionFailed && condition.Status == metav1.ConditionTrue && condition.Reason == "PodReadFailed" {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("temporary read failure was not exposed in status: %#v", diagnosed.Status.Conditions)
	}

	result, err := reconciler.Reconcile(ctx, requestFor(resource))
	if err != nil {
		t.Fatalf("reconcile did not recover after temporary read failure: %v", err)
	}
	if result.RequeueAfter != 2*time.Minute {
		t.Fatalf("expected recovered reconcile to preserve deadline, got %s", result.RequeueAfter)
	}
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &diagnosed); err != nil {
		t.Fatal(err)
	}
	for _, condition := range diagnosed.Status.Conditions {
		if condition.Type == ConditionFailed && condition.Status != metav1.ConditionFalse {
			t.Fatalf("successful retry did not clear failure condition: %#v", condition)
		}
	}
}

func TestTerminatingPodRemainsDeletingUntilItDisappears(t *testing.T) {
	ctx := context.Background()
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	resource := testResource()
	pod := ownedPod(resource, started)
	pod.Finalizers = []string{"example.test/slow-shutdown"}
	resource.Status = CjPodStatus{
		Phase:     PhaseDeleting,
		PodUID:    pod.UID,
		StartedAt: &metav1.Time{Time: started},
	}
	scheme := testScheme(t)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource, pod).Build()
	countingClient := &deleteFailingClient{Client: baseClient}
	reconciler := &CjPodReconciler{Client: countingClient, Scheme: scheme, Clock: &fakeClock{now: started.Add(PodLifetime + time.Minute)}}

	result, err := reconciler.Reconcile(ctx, requestFor(resource))
	if err != nil {
		t.Fatalf("reconcile returned error for terminating Pod: %v", err)
	}
	if result.RequeueAfter != deletionPollDelay {
		t.Fatalf("expected deletion polling, got %s", result.RequeueAfter)
	}
	var terminating corev1.Pod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &terminating); err != nil {
		t.Fatalf("finalized Pod should still exist: %v", err)
	}
	if terminating.DeletionTimestamp == nil {
		t.Fatal("expected Pod to be terminating")
	}
	var current CjPod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase != PhaseDeleting {
		t.Fatalf("controller completed before Pod disappeared: %#v", current.Status)
	}

	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); err != nil {
		t.Fatalf("polling terminating Pod returned error: %v", err)
	}
	if countingClient.calls != 1 {
		t.Fatalf("expected exactly one Pod delete request, got %d", countingClient.calls)
	}
}

func TestPodCreationFailureIsVisibleInStatus(t *testing.T) {
	ctx := context.Background()
	resource := testResource()
	scheme := testScheme(t)
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&CjPod{}).WithObjects(resource).Build()
	injectedError := errors.New("injected Pod admission failure")
	failingClient := &podCreateFailingClient{Client: baseClient, failures: 1, err: injectedError}
	reconciler := &CjPodReconciler{Client: failingClient, Scheme: scheme, Clock: &fakeClock{now: time.Now()}}

	if _, err := reconciler.Reconcile(ctx, requestFor(resource)); !errors.Is(err, injectedError) {
		t.Fatalf("expected injected Pod creation failure, got %v", err)
	}
	var diagnosed CjPod
	if err := baseClient.Get(ctx, requestFor(resource).NamespacedName, &diagnosed); err != nil {
		t.Fatal(err)
	}
	condition := findCondition(diagnosed.Status.Conditions, ConditionFailed)
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.Reason != "PodCreateFailed" || !strings.Contains(condition.Message, injectedError.Error()) {
		t.Fatalf("Pod creation failure was not exposed in status: %#v", diagnosed.Status.Conditions)
	}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
