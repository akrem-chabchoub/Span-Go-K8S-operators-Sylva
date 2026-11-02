package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	spanv1alpha1 "github.com/akrem/span/api/v1alpha1"
	"github.com/akrem/span/internal/helper"
)

var k8sClient client.Client

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	ctx, cancel := context.WithCancel(context.Background())
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start envtest: %v\n", err)
		os.Exit(1)
	}

	sch := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(sch); err != nil {
		panic(err)
	}
	if err := spanv1alpha1.AddToScheme(sch); err != nil {
		panic(err)
	}
	k8sClient, err = client.New(cfg, client.Options{Scheme: sch})
	if err != nil {
		panic(err)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 sch,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		panic(err)
	}
	reconciler := &SegmentReconciler{
		Client:   mgr.GetClient(),
		Reader:   mgr.GetAPIReader(),
		Recorder: mgr.GetEventRecorderFor("span"),
		Helper:   scriptedHelper{},
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		panic(err)
	}
	go func() {
		if err := mgr.Start(ctx); err != nil {
			panic(err)
		}
	}()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		panic("cache did not sync")
	}

	code := m.Run()
	cancel()
	if err := testEnv.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop envtest: %v\n", err)
	}
	os.Exit(code)
}

func TestReadySegment(t *testing.T) {
	vlan := int32(100)
	seg := createSegment(t, "lab", "10.20.30.0/24", "10.20.30.1", "lab", &vlan)
	got := waitReason(t, client.ObjectKeyFromObject(seg), spanv1alpha1.ReasonPlanned)

	if !containsString(got.Finalizers, spanv1alpha1.Finalizer) {
		t.Fatalf("finalizers %v", got.Finalizers)
	}
	if got.Annotations[spanv1alpha1.IdentityAnnotation] != "10.20.30.0/24|10.20.30.1|lab" {
		t.Fatalf("identity %v", got.Annotations)
	}
	if len(got.Status.Plan) != 2 || got.Status.Plan[0].Action != spanv1alpha1.ActionAddVLAN || got.Status.Plan[1].Address != "10.20.30.1/24" {
		t.Fatalf("plan %#v", got.Status.Plan)
	}
	applied := meta.FindStatusCondition(got.Status.Conditions, spanv1alpha1.ConditionApplied)
	if applied == nil || applied.Status != metav1.ConditionTrue || applied.Reason != spanv1alpha1.ReasonApplied {
		t.Fatalf("applied %#v", applied)
	}
	if len(got.Status.Observed) != 1 || got.Status.Observed[0].Link != "span0" || !containsString(got.Status.Observed[0].Addresses, "10.20.30.1/24") {
		t.Fatalf("observed %#v", got.Status.Observed)
	}
}

type scriptedHelper struct{}

func (scriptedHelper) Apply(_ context.Context, req helper.Request) ([]helper.Observation, error) {
	var addresses []string
	for _, op := range req.Ops {
		if op.Action == spanv1alpha1.ActionAddAddress {
			addresses = append(addresses, op.Address)
		}
	}
	return []helper.Observation{{
		Endpoint:  "memory",
		Link:      req.Link,
		Addresses: addresses,
	}}, nil
}

func (scriptedHelper) Remove(context.Context, helper.Request) ([]helper.Observation, error) {
	return []helper.Observation{{Endpoint: "memory"}}, nil
}

func TestInvalidCIDR(t *testing.T) {
	seg := createSegment(t, "bad", "nope", "10.0.0.1", "lab", nil)
	got := waitReason(t, client.ObjectKeyFromObject(seg), spanv1alpha1.ReasonInvalid)
	if len(got.Status.Plan) != 0 {
		t.Fatalf("plan %#v", got.Status.Plan)
	}
	if containsString(got.Finalizers, spanv1alpha1.Finalizer) {
		t.Fatal("invalid segment should not take a finalizer")
	}
}

func TestOverlapOlderWins(t *testing.T) {
	older := createSegment(t, "older", "10.51.0.0/16", "10.51.0.1", "lab", nil)
	waitReason(t, client.ObjectKeyFromObject(older), spanv1alpha1.ReasonPlanned)
	// creationTimestamp is one-second resolution. A same-second tie falls back to the name,
	// and "newer" sorts before "older".
	time.Sleep(2 * time.Second)

	newer := createSegment(t, "newer", "10.51.8.0/24", "10.51.8.1", "lab", nil)
	got := waitReason(t, client.ObjectKeyFromObject(newer), spanv1alpha1.ReasonOverlap)
	if len(got.Status.Plan) != 0 {
		t.Fatalf("loser plan %#v", got.Status.Plan)
	}

	if err := k8sClient.Delete(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	waitGone(t, client.ObjectKeyFromObject(older))
	waitReason(t, client.ObjectKeyFromObject(newer), spanv1alpha1.ReasonPlanned)
}

func TestDeleteRemovesFinalizer(t *testing.T) {
	seg := createSegment(t, "gone", "10.52.0.0/24", "10.52.0.1", "lab", nil)
	waitReason(t, client.ObjectKeyFromObject(seg), spanv1alpha1.ReasonPlanned)
	if err := k8sClient.Delete(context.Background(), seg); err != nil {
		t.Fatal(err)
	}
	waitGone(t, client.ObjectKeyFromObject(seg))
}

func TestImmutableGateway(t *testing.T) {
	seg := createSegment(t, "fixed", "10.53.0.0/24", "10.53.0.1", "lab", nil)
	got := waitReason(t, client.ObjectKeyFromObject(seg), spanv1alpha1.ReasonPlanned)

	got.Spec.Gateway = "10.53.0.2"
	if err := k8sClient.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	next := waitReason(t, client.ObjectKeyFromObject(seg), spanv1alpha1.ReasonImmutable)
	if len(next.Status.Plan) != 1 || next.Status.Plan[0].Address != "10.53.0.1/24" {
		t.Fatalf("plan changed %#v", next.Status.Plan)
	}
}

func createSegment(t *testing.T, name, cidr, gateway, cluster string, vlan *int32) *spanv1alpha1.Segment {
	t.Helper()
	ns := namespace(t)
	seg := &spanv1alpha1.Segment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: spanv1alpha1.SegmentSpec{
			CIDR:    cidr,
			Gateway: gateway,
			Cluster: cluster,
			VLAN:    vlan,
		},
	}
	if err := k8sClient.Create(context.Background(), seg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := k8sClient.Delete(context.Background(), seg)
		if err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleanup segment: %v", err)
		}
	})
	return seg
}

func namespace(t *testing.T) string {
	t.Helper()
	name := "ns-" + sanitize(t.Name())
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8sClient.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), ns)
	})
	return name
}

func sanitize(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+'a'-'A')
		default:
			out = append(out, '-')
		}
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return string(out)
}

func waitReason(t *testing.T, key types.NamespacedName, reason string) *spanv1alpha1.Segment {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last spanv1alpha1.Segment
	for time.Now().Before(deadline) {
		err := k8sClient.Get(context.Background(), key, &last)
		if err == nil {
			cond := meta.FindStatusCondition(last.Status.Conditions, spanv1alpha1.ConditionReady)
			if cond != nil && cond.Reason == reason {
				return last.DeepCopy()
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s reason %s, last status: %#v", key, reason, last.Status)
	return nil
}

func waitGone(t *testing.T, key types.NamespacedName) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		err := k8sClient.Get(context.Background(), key, &spanv1alpha1.Segment{})
		if apierrors.IsNotFound(err) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("segment %s was not deleted", key)
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
