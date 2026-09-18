//go:build integration

package cjpod

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func TestManagerLifecycleWithAPIServer(t *testing.T) {
	testEnvironment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("config", "crd")},
		ErrorIfCRDPathMissing: true,
	}
	restConfig, err := testEnvironment.Start()
	if err != nil {
		t.Fatalf("start envtest (run `make test-integration` to install its API-server assets): %v", err)
	}
	t.Cleanup(func() {
		if err := testEnvironment.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t, restConfig, scheme, 2*time.Second)
	managerContext, stopManager := context.WithCancel(context.Background())
	managerErrors := make(chan error, 1)
	go func() { managerErrors <- manager.Start(managerContext) }()
	if !manager.GetCache().WaitForCacheSync(managerContext) {
		t.Fatal("manager cache did not synchronize")
	}

	apiClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "cjpod-integration"}}
	if err := apiClient.Create(ctx, namespace); err != nil {
		t.Fatal(err)
	}
	invalid := testResource()
	invalid.Name = "invalid-empty-template"
	invalid.Namespace = namespace.Name
	invalid.UID = ""
	invalid.Spec.Template.Spec.Containers = nil
	if err := apiClient.Create(ctx, invalid); !apierrors.IsInvalid(err) {
		t.Fatalf("CRD should reject a Pod template without containers, got %v", err)
	}
	missingImage := testResource()
	missingImage.Name = "invalid-empty-image"
	missingImage.Namespace = namespace.Name
	missingImage.UID = ""
	missingImage.Spec.Template.Spec.Containers[0].Image = ""
	if err := apiClient.Create(ctx, missingImage); !apierrors.IsInvalid(err) {
		t.Fatalf("CRD should reject a container without an image, got %v", err)
	}
	resource := testResource()
	resource.Namespace = namespace.Name
	resource.UID = ""
	resource.ResourceVersion = ""
	if err := apiClient.Create(ctx, resource); err != nil {
		t.Fatal(err)
	}

	key := types.NamespacedName{Name: resource.Name, Namespace: resource.Namespace}
	eventually(t, 10*time.Second, func() bool {
		var pod corev1.Pod
		if apiClient.Get(ctx, key, &pod) != nil || !metav1.IsControlledBy(&pod, resource) {
			return false
		}
		var current CjPod
		if apiClient.Get(ctx, key, &current) != nil {
			return false
		}
		ready := findCondition(current.Status.Conditions, ConditionReady)
		completed := findCondition(current.Status.Conditions, ConditionCompleted)
		return ready != nil && ready.Status == metav1.ConditionTrue && completed != nil && completed.Status == metav1.ConditionFalse
	}, "controller did not create the owned Pod")

	// Stop the whole manager, not just a reconciler call, then start a fresh
	// manager against the same API server. The persisted Pod timestamp remains
	// the deadline anchor after the restart.
	stopManager()
	select {
	case err := <-managerErrors:
		if err != nil {
			t.Fatalf("first manager stopped with an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first manager did not stop")
	}
	restartedManager := newTestManager(t, restConfig, scheme, 2*time.Second)
	restartedContext, stopRestartedManager := context.WithCancel(context.Background())
	t.Cleanup(stopRestartedManager)
	restartedErrors := make(chan error, 1)
	go func() { restartedErrors <- restartedManager.Start(restartedContext) }()
	if !restartedManager.GetCache().WaitForCacheSync(restartedContext) {
		t.Fatal("restarted manager cache did not synchronize")
	}

	eventually(t, 10*time.Second, func() bool {
		var current CjPod
		if apiClient.Get(ctx, key, &current) != nil {
			return false
		}
		var pod corev1.Pod
		podMissing := apierrors.IsNotFound(apiClient.Get(ctx, key, &pod))
		completed := findCondition(current.Status.Conditions, ConditionCompleted)
		return podMissing && current.Status.Phase == PhaseCompleted && current.Status.CompletedAt != nil && completed != nil && completed.Status == metav1.ConditionTrue
	}, "controller did not delete the Pod and persist Completed status")

	var completed CjPod
	if err := apiClient.Get(ctx, key, &completed); err != nil {
		t.Fatal(err)
	}
	completed.Spec.Template.Spec.Containers[0].Image = "nginx:changed"
	if err := apiClient.Update(ctx, &completed); !apierrors.IsInvalid(err) {
		t.Fatalf("CRD should reject changes to the one-shot spec, got %v", err)
	}

	externallyDeleted := testResource()
	externallyDeleted.Name = "externally-deleted"
	externallyDeleted.Namespace = namespace.Name
	externallyDeleted.UID = ""
	if err := apiClient.Create(ctx, externallyDeleted); err != nil {
		t.Fatal(err)
	}
	externalKey := types.NamespacedName{Name: externallyDeleted.Name, Namespace: externallyDeleted.Namespace}
	eventually(t, 10*time.Second, func() bool {
		var pod corev1.Pod
		return apiClient.Get(ctx, externalKey, &pod) == nil
	}, "controller did not create the external-deletion test Pod")
	var externalPod corev1.Pod
	if err := apiClient.Get(ctx, externalKey, &externalPod); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Delete(ctx, &externalPod); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() bool {
		var current CjPod
		if apiClient.Get(ctx, externalKey, &current) != nil {
			return false
		}
		return current.Status.Phase == PhaseFailed
	}, "external Pod deletion did not produce terminal Failed status")
	var replacement corev1.Pod
	if err := apiClient.Get(ctx, externalKey, &replacement); !apierrors.IsNotFound(err) {
		t.Fatalf("controller replaced an externally deleted Pod: %v", err)
	}

	select {
	case err := <-restartedErrors:
		if err != nil {
			t.Fatalf("restarted manager stopped unexpectedly: %v", err)
		}
	default:
	}
}

func newTestManager(t *testing.T, restConfig *rest.Config, scheme *runtime.Scheme, lifetime time.Duration) ctrl.Manager {
	t.Helper()
	skipNameValidation := true
	manager, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:     scheme,
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: controllerconfig.Controller{SkipNameValidation: &skipNameValidation},
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &CjPodReconciler{
		Client:   manager.GetClient(),
		Scheme:   manager.GetScheme(),
		Lifetime: lifetime,
		Recorder: manager.GetEventRecorderFor("cjpod-envtest"),
	}
	if err := reconciler.SetupWithManager(manager); err != nil {
		t.Fatalf("setup controller: %v", err)
	}
	return manager
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(message)
}
