package main

import (
	"flag"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"

	spanv1 "github.com/akrem/span/api/v1"
	spanv1alpha1 "github.com/akrem/span/api/v1alpha1"
	"github.com/akrem/span/internal/controller"
	"github.com/akrem/span/internal/helper"
	spanwebhook "github.com/akrem/span/internal/webhook"
)

func main() {
	var (
		metricsAddr     string
		probeAddr       string
		certDir         string
		enableWebhook   bool
		leaderElect     bool
		helperEndpoints string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The metrics endpoint. Set to 0 to disable it.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The health endpoint. Set to 0 to disable it.")
	flag.StringVar(&certDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs", "Directory containing tls.crt and tls.key.")
	flag.BoolVar(&enableWebhook, "enable-webhook", true, "Serve the validating webhook. Requires certificates in webhook-cert-dir.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Elect a leader before running. Turn this on before running more than one replica.")
	flag.StringVar(&helperEndpoints, "helper-endpoints", "", "Comma-separated helper URLs. Empty leaves Applied pending.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(spanv1alpha1.AddToScheme(scheme))
	utilruntime.Must(spanv1.AddToScheme(scheme))

	setupLog := ctrl.Log.WithName("setup")
	options := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "span-operator.span.dev",
	}
	if enableWebhook {
		options.WebhookServer = webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: certDir,
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		setupLog.Error(err, "start manager")
		os.Exit(1)
	}

	reconciler := &controller.SegmentReconciler{
		Client:   mgr.GetClient(),
		Reader:   mgr.GetAPIReader(),
		Recorder: mgr.GetEventRecorderFor("span"),
		Helper:   helperPool(helperEndpoints),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "start segment controller")
		os.Exit(1)
	}
	if enableWebhook {
		if err := (&spanwebhook.SegmentValidator{}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "start segment webhook")
			os.Exit(1)
		}
		mgr.GetWebhookServer().Register("/convert", conversion.NewWebhookHandler(mgr.GetScheme()))
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "ready check")
		os.Exit(1)
	}

	setupLog.Info("starting", "webhook", enableWebhook, "helpers", helperEndpoints)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "run manager")
		os.Exit(1)
	}
}

func helperPool(raw string) *helper.Pool {
	var endpoints []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			endpoints = append(endpoints, part)
		}
	}
	if len(endpoints) == 0 {
		return nil
	}
	return &helper.Pool{Endpoints: endpoints}
}
