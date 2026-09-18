package main

import (
	"flag"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	cjpod "github.com/alexander-samay/cj-senior-platform-exercise"
)

var scheme = runtime.NewScheme()

const leaderElectionID = "cjpod-controller.interview.cj.dev"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(cjpod.AddToScheme(scheme))
}

func managerOptions(metricsAddress, probeAddress string, leaderElection bool) ctrl.Options {
	return ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddress},
		HealthProbeBindAddress: probeAddress,
		LeaderElection:         leaderElection,
		LeaderElectionID:       leaderElectionID,
	}
}

func main() {
	var metricsAddress string
	var probeAddress string
	var leaderElection bool

	flag.StringVar(&metricsAddress, "metrics-bind-address", ":8080", "Address for the metrics endpoint.")
	flag.StringVar(&probeAddress, "health-probe-bind-address", ":8081", "Address for health probes.")
	flag.BoolVar(&leaderElection, "leader-elect", false, "Enable leader election for controller manager availability.")
	loggingOptions := zap.Options{Development: false}
	loggingOptions.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&loggingOptions)))
	setupLog := ctrl.Log.WithName("setup")

	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOptions(metricsAddress, probeAddress, leaderElection))
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	reconciler := &cjpod.CjPodReconciler{
		Client:   manager.GetClient(),
		Scheme:   manager.GetScheme(),
		Recorder: manager.GetEventRecorderFor("cjpod-controller"),
	}
	if err := reconciler.SetupWithManager(manager); err != nil {
		setupLog.Error(err, "unable to create controller")
		os.Exit(1)
	}
	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to add health check")
		os.Exit(1)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to add readiness check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := manager.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager stopped with an error")
		os.Exit(1)
	}
}
