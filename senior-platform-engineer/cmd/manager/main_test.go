package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	cjpod "github.com/alexander-samay/cj-senior-platform-exercise"
)

func TestManagerOptions(t *testing.T) {
	options := managerOptions(":9090", ":9091", true)
	if options.Scheme != scheme {
		t.Fatal("manager does not use the configured scheme")
	}
	if options.Metrics.BindAddress != ":9090" || options.HealthProbeBindAddress != ":9091" {
		t.Fatalf("manager endpoints were not preserved: %#v", options)
	}
	if !options.LeaderElection || options.LeaderElectionID != leaderElectionID {
		t.Fatalf("leader election options were not preserved: %#v", options)
	}
}

func TestManagerSchemeContainsManagedTypes(t *testing.T) {
	knownTypes := []schema.GroupVersionKind{
		{Group: cjpod.GroupVersion.Group, Version: cjpod.GroupVersion.Version, Kind: "CjPod"},
		{Group: "", Version: "v1", Kind: "Pod"},
	}
	for _, gvk := range knownTypes {
		if !scheme.Recognizes(gvk) {
			t.Fatalf("manager scheme does not recognize %s", gvk)
		}
	}
	if _, err := scheme.New(corev1.SchemeGroupVersion.WithKind("Pod")); err != nil {
		t.Fatalf("manager scheme cannot construct Pods: %v", err)
	}
}
