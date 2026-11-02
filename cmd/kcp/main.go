package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	spanv1alpha1 "github.com/akrem/span/api/v1alpha1"
	"github.com/akrem/span/internal/controller"
)

func main() {
	var endpointSlice string
	flag.StringVar(&endpointSlice, "endpointslice", "span", "APIExportEndpointSlice that lists the workspaces bound to the span export.")
	zapOpts := zap.Options{Development: true}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(spanv1alpha1.AddToScheme(scheme))
	utilruntime.Must(apisv1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
	utilruntime.Must(tenancyv1alpha1.AddToScheme(scheme))

	cfg := ctrl.GetConfigOrDie()
	provider, err := apiexport.New(cfg, endpointSlice, apiexport.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "apiexport provider: %v\n", err)
		os.Exit(1)
	}
	mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: ":8090"},
		HealthProbeBindAddress: ":8091",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "manager: %v\n", err)
		os.Exit(1)
	}

	// One reconciler per request. A shared client would mix workspaces.
	// No helper here: kcp records the plan. The cluster operator programs the host.
	err = mcbuilder.ControllerManagedBy(mgr).
		Named("segment").
		For(&spanv1alpha1.Segment{}).
		Complete(mcreconcile.Func(func(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
			cl, err := mgr.GetCluster(ctx, req.ClusterName)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("get cluster %s: %w", req.ClusterName, err)
			}
			r := &controller.SegmentReconciler{
				Client:   cl.GetClient(),
				Reader:   cl.GetAPIReader(),
				Recorder: cl.GetEventRecorderFor("span"),
			}
			return r.Reconcile(ctx, reconcile.Request{NamespacedName: req.NamespacedName})
		}))
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller: %v\n", err)
		os.Exit(1)
	}
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
}
