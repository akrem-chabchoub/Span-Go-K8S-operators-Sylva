package controller

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spanv1alpha1 "github.com/akrem/span/api/v1alpha1"
	"github.com/akrem/span/internal/helper"
)

func TestNilHelperStaysPending(t *testing.T) {
	ctx := context.Background()
	seg := sampleSegment("pending", "10.70.0.0/24", "10.70.0.1")
	r, c := directReconciler(t, seg, nil)
	if _, err := r.Reconcile(ctx, requestFor(seg)); err != nil {
		t.Fatal(err)
	}
	got := getSegment(t, c, seg)
	applied := meta.FindStatusCondition(got.Status.Conditions, spanv1alpha1.ConditionApplied)
	if applied == nil || applied.Status != metav1.ConditionFalse || applied.Reason != spanv1alpha1.ReasonPendingHelper {
		t.Fatalf("applied %#v", applied)
	}
}

func TestHelperFailureKeepsFinalizer(t *testing.T) {
	ctx := context.Background()
	seg := sampleSegment("down", "10.71.0.0/24", "10.71.0.1")
	r, c := directReconciler(t, seg, failHelper{})
	res, err := r.Reconcile(ctx, requestFor(seg))
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != helperRetry {
		t.Fatalf("requeue %s", res.RequeueAfter)
	}
	got := getSegment(t, c, seg)
	applied := meta.FindStatusCondition(got.Status.Conditions, spanv1alpha1.ConditionApplied)
	if applied == nil || applied.Reason != spanv1alpha1.ReasonHelperFailed {
		t.Fatalf("applied %#v", applied)
	}
	if !containsString(got.Finalizers, spanv1alpha1.Finalizer) {
		t.Fatal("finalizer missing")
	}

	if err := c.Delete(ctx, got); err != nil {
		t.Fatal(err)
	}
	res, err = r.Reconcile(ctx, requestFor(seg))
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != helperRetry {
		t.Fatalf("delete requeue %s", res.RequeueAfter)
	}
	still := getSegment(t, c, seg)
	if !containsString(still.Finalizers, spanv1alpha1.Finalizer) {
		t.Fatal("finalizer was dropped while the helper was failing")
	}
}

type failHelper struct{}

func (failHelper) Apply(context.Context, helper.Request) ([]helper.Observation, error) {
	return nil, errors.New("link is down")
}

func (failHelper) Remove(context.Context, helper.Request) ([]helper.Observation, error) {
	return nil, errors.New("link is down")
}

func directReconciler(t *testing.T, seg *spanv1alpha1.Segment, host helper.Applier) (*SegmentReconciler, client.Client) {
	t.Helper()
	sch := runtime.NewScheme()
	if err := spanv1alpha1.AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(sch).WithStatusSubresource(seg).WithObjects(seg).Build()
	return &SegmentReconciler{Client: c, Reader: c, Helper: host}, c
}

func sampleSegment(name, cidr, gateway string) *spanv1alpha1.Segment {
	return &spanv1alpha1.Segment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name)},
		Spec: spanv1alpha1.SegmentSpec{
			CIDR:    cidr,
			Gateway: gateway,
			Cluster: "lab",
		},
	}
}

func requestFor(seg *spanv1alpha1.Segment) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: seg.Namespace, Name: seg.Name}}
}

func getSegment(t *testing.T, c client.Client, seg *spanv1alpha1.Segment) *spanv1alpha1.Segment {
	t.Helper()
	var got spanv1alpha1.Segment
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: seg.Namespace, Name: seg.Name}, &got); err != nil {
		t.Fatal(err)
	}
	return &got
}
