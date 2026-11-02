package webhook

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	spanv1alpha1 "github.com/akrem/span/api/v1alpha1"
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	os.Exit(m.Run())
}

func TestWebhook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "config", "webhook")},
		},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})

	sch := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	if err := spanv1alpha1.AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	k8s, err := client.New(cfg, client.Options{Scheme: sch})
	if err != nil {
		t.Fatal(err)
	}

	wh := testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: sch,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		HealthProbeBindAddress: "0",
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    wh.LocalServingHost,
			Port:    wh.LocalServingPort,
			CertDir: wh.LocalServingCertDir,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&SegmentValidator{}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "manager: %v\n", err)
		}
	}()
	waitForTLS(t, wh.LocalServingHost, wh.LocalServingPort)

	good := segment("good", "10.60.0.0/24", "10.60.0.1")
	if err := k8s.Create(ctx, good); err != nil {
		t.Fatal(err)
	}

	overlap := segment("overlap", "10.60.0.0/16", "10.60.0.1")
	err = k8s.Create(ctx, overlap)
	if !apierrors.IsInvalid(err) {
		t.Fatalf("overlap create err = %v", err)
	}

	bad := segment("bad", "10.61.0.0/24", "10.61.1.1")
	err = k8s.Create(ctx, bad)
	if !apierrors.IsInvalid(err) {
		t.Fatalf("bad gateway err = %v", err)
	}

	vlan := int32(20)
	good.Spec.VLAN = &vlan
	if err := k8s.Update(ctx, good); err != nil {
		t.Fatal(err)
	}

	current := &spanv1alpha1.Segment{}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(good), current); err != nil {
		t.Fatal(err)
	}
	current.Spec.Gateway = "10.60.0.2"
	err = k8s.Update(ctx, current)
	if !apierrors.IsInvalid(err) {
		t.Fatalf("gateway change err = %v", err)
	}
}

func segment(name, cidr, gateway string) *spanv1alpha1.Segment {
	return &spanv1alpha1.Segment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: spanv1alpha1.SegmentSpec{
			CIDR:    cidr,
			Gateway: gateway,
			Cluster: "lab",
		},
	}
}

func waitForTLS(t *testing.T, host string, port int) {
	t.Helper()
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // the test only checks that the port accepts TLS
		if err == nil {
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("webhook never listened on %s", addr)
}
