package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	spanv1alpha1 "github.com/akrem/span/api/v1alpha1"
	"github.com/akrem/span/internal/helper"
	"github.com/akrem/span/internal/planner"
)

const (
	defaultOverlapRequeue = 30 * time.Second
	helperRetry           = 10 * time.Second
)

var reconcileTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "span_segment_reconcile_total",
	Help: "Segment reconciles by outcome. The label is an outcome, never a segment name.",
}, []string{"result"})

func init() {
	metrics.Registry.MustRegister(reconcileTotal)
}

type SegmentReconciler struct {
	client.Client
	Reader         client.Reader
	Recorder       record.EventRecorder
	Helper         helper.Applier
	OverlapRequeue time.Duration
}

func (r *SegmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var seg spanv1alpha1.Segment
	if err := r.Get(ctx, req.NamespacedName, &seg); err != nil {
		if apierrors.IsNotFound(err) {
			reconcileTotal.WithLabelValues("notfound").Inc()
			return ctrl.Result{}, nil
		}
		reconcileTotal.WithLabelValues("error").Inc()
		return ctrl.Result{}, fmt.Errorf("get segment: %w", err)
	}

	if !seg.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &seg)
	}

	spec := specOf(&seg)
	planned, errs := planner.Plan(spec)
	if len(errs) > 0 {
		msg := errs.ToAggregate().Error()
		if res, err := r.unwind(ctx, &seg); err != nil || res.RequeueAfter > 0 {
			return res, err
		}
		return r.finish(ctx, &seg, func(s *spanv1alpha1.Segment) {
			s.Status.Plan = nil
			s.Status.ConnectedRoute = ""
			s.Status.Observed = nil
			setReady(s, metav1.ConditionFalse, spanv1alpha1.ReasonInvalid, msg)
			setApplied(s, metav1.ConditionFalse, spanv1alpha1.ReasonNotPlanned, "Spec is invalid, so nothing will be applied.")
		}, "invalid", ctrl.Result{})
	}

	stored := seg.Annotations[spanv1alpha1.IdentityAnnotation]
	if stored != "" && stored != planned.Identity {
		return r.finish(ctx, &seg, func(s *spanv1alpha1.Segment) {
			setReady(s, metav1.ConditionFalse, spanv1alpha1.ReasonImmutable,
				"cidr, gateway, and cluster cannot change; delete the segment and create a new one")
			setApplied(s, metav1.ConditionFalse, spanv1alpha1.ReasonNotPlanned, "The original plan is unchanged so a later cleanup still knows the old address.")
		}, "immutable", ctrl.Result{})
	}

	if err := r.ensurePromise(ctx, &seg, planned.Identity); err != nil {
		reconcileTotal.WithLabelValues("error").Inc()
		return ctrl.Result{}, err
	}

	claims, err := r.listClaims(ctx)
	if err != nil {
		reconcileTotal.WithLabelValues("error").Inc()
		return ctrl.Result{}, fmt.Errorf("list segments: %w", err)
	}
	if blocker, blocked := planner.BlockedBy(claimOf(&seg), claims, false); blocked {
		msg := fmt.Sprintf("overlaps %s/%s (%s), which keeps the prefix", blocker.Namespace, blocker.Name, blocker.CIDR)
		if res, err := r.unwind(ctx, &seg); err != nil || res.RequeueAfter > 0 {
			return res, err
		}
		return r.finish(ctx, &seg, func(s *spanv1alpha1.Segment) {
			s.Status.Plan = nil
			s.Status.ConnectedRoute = ""
			s.Status.Observed = nil
			setReady(s, metav1.ConditionFalse, spanv1alpha1.ReasonOverlap, msg)
			setApplied(s, metav1.ConditionFalse, spanv1alpha1.ReasonNotPlanned, "This segment lost the prefix, so nothing will be applied.")
		}, "overlap", ctrl.Result{RequeueAfter: r.overlapRequeue()})
	}

	obs, applyErr := r.apply(ctx, &seg, planned)
	metric := "ready"
	res := ctrl.Result{}
	if r.Helper != nil && applyErr != nil {
		metric = "error"
		res = ctrl.Result{RequeueAfter: helperRetry}
	}
	return r.finish(ctx, &seg, func(s *spanv1alpha1.Segment) {
		s.Status.Plan = apiOps(planned.Ops)
		s.Status.ConnectedRoute = planned.ConnectedRoute
		s.Status.Observed = obs
		setReady(s, metav1.ConditionTrue, spanv1alpha1.ReasonPlanned, "This segment owns the prefix.")
		switch {
		case r.Helper == nil:
			setApplied(s, metav1.ConditionFalse, spanv1alpha1.ReasonPendingHelper, "No host helper is installed yet, so the plan stays on the object.")
		case applyErr != nil:
			setApplied(s, metav1.ConditionFalse, spanv1alpha1.ReasonHelperFailed, applyErr.Error())
		default:
			setApplied(s, metav1.ConditionTrue, spanv1alpha1.ReasonApplied, "The helper programmed the link and reported what the kernel has.")
		}
	}, metric, res)
}

func (r *SegmentReconciler) finalize(ctx context.Context, seg *spanv1alpha1.Segment) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(seg, spanv1alpha1.Finalizer) {
		reconcileTotal.WithLabelValues("deleted").Inc()
		return ctrl.Result{}, nil
	}
	if res, err := r.unwind(ctx, seg); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	controllerutil.RemoveFinalizer(seg, spanv1alpha1.Finalizer)
	if err := r.Update(ctx, seg); err != nil {
		if apierrors.IsNotFound(err) {
			reconcileTotal.WithLabelValues("deleted").Inc()
			return ctrl.Result{}, nil
		}
		reconcileTotal.WithLabelValues("error").Inc()
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	reconcileTotal.WithLabelValues("deleted").Inc()
	return ctrl.Result{}, nil
}

// unwind asks the helper to delete what this object previously programmed.
// A helper error keeps the plan and the finalizer so the next pass can retry.
func (r *SegmentReconciler) unwind(ctx context.Context, seg *spanv1alpha1.Segment) (ctrl.Result, error) {
	if r.Helper == nil || len(seg.Status.Plan) == 0 {
		return ctrl.Result{}, nil
	}
	links := programmedLinks(seg)
	if len(links) == 0 {
		links = []string{linkOf(seg)}
	}
	var failed error
	for _, link := range links {
		if _, err := r.Helper.Remove(ctx, helperRequest(link, seg.Status.Plan)); err != nil && failed == nil {
			failed = err
		}
	}
	if failed == nil {
		return ctrl.Result{}, nil
	}
	return r.finish(ctx, seg, func(s *spanv1alpha1.Segment) {
		setApplied(s, metav1.ConditionFalse, spanv1alpha1.ReasonHelperFailed, failed.Error())
	}, "error", ctrl.Result{RequeueAfter: helperRetry})
}

func (r *SegmentReconciler) apply(ctx context.Context, seg *spanv1alpha1.Segment, planned planner.Result) ([]spanv1alpha1.HostObservation, error) {
	if r.Helper == nil {
		return nil, nil
	}
	for _, link := range programmedLinks(seg) {
		if link == planned.Link {
			continue
		}
		if _, err := r.Helper.Remove(ctx, helperRequest(link, seg.Status.Plan)); err != nil {
			return nil, err
		}
	}
	obs, err := r.Helper.Apply(ctx, helperRequest(planned.Link, apiOps(planned.Ops)))
	return apiObserved(obs), err
}

func (r *SegmentReconciler) ensurePromise(ctx context.Context, seg *spanv1alpha1.Segment, identity string) error {
	dirty := false
	if seg.Annotations == nil {
		seg.Annotations = map[string]string{}
	}
	if seg.Annotations[spanv1alpha1.IdentityAnnotation] == "" {
		seg.Annotations[spanv1alpha1.IdentityAnnotation] = identity
		dirty = true
	}
	if !controllerutil.ContainsFinalizer(seg, spanv1alpha1.Finalizer) {
		controllerutil.AddFinalizer(seg, spanv1alpha1.Finalizer)
		dirty = true
	}
	if !dirty {
		return nil
	}
	if err := r.Update(ctx, seg); err != nil {
		return fmt.Errorf("save identity: %w", err)
	}
	return nil
}

func (r *SegmentReconciler) finish(
	ctx context.Context,
	seg *spanv1alpha1.Segment,
	mutate func(*spanv1alpha1.Segment),
	metric string,
	res ctrl.Result,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	orig := seg.Status.DeepCopy()
	prev := meta.FindStatusCondition(seg.Status.Conditions, spanv1alpha1.ConditionReady)
	mutate(seg)
	seg.Status.ObservedGeneration = seg.Generation

	next := meta.FindStatusCondition(seg.Status.Conditions, spanv1alpha1.ConditionReady)
	if r.Recorder != nil && next != nil && (prev == nil || prev.Status != next.Status || prev.Reason != next.Reason) {
		eventType := corev1.EventTypeNormal
		if next.Status != metav1.ConditionTrue {
			eventType = corev1.EventTypeWarning
		}
		r.Recorder.Event(seg, eventType, next.Reason, next.Message)
	}

	if orig != nil && equality.Semantic.DeepEqual(*orig, seg.Status) {
		reconcileTotal.WithLabelValues("unchanged").Inc()
		logger.V(1).Info("segment unchanged")
		return res, nil
	}
	if err := r.Status().Update(ctx, seg); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		reconcileTotal.WithLabelValues("error").Inc()
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}
	reconcileTotal.WithLabelValues(metric).Inc()
	logger.Info("segment reconciled", "result", metric)
	return res, nil
}

func (r *SegmentReconciler) listClaims(ctx context.Context) ([]planner.Claim, error) {
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	var list spanv1alpha1.SegmentList
	if err := reader.List(ctx, &list); err != nil {
		return nil, err
	}
	claims := make([]planner.Claim, 0, len(list.Items))
	for i := range list.Items {
		claims = append(claims, claimOf(&list.Items[i]))
	}
	return claims, nil
}

func (r *SegmentReconciler) overlapRequeue() time.Duration {
	if r.OverlapRequeue > 0 {
		return r.OverlapRequeue
	}
	return defaultOverlapRequeue
}

func (r *SegmentReconciler) enqueueAll(ctx context.Context, obj client.Object) []reconcile.Request {
	claims := []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()},
	}}
	var list spanv1alpha1.SegmentList
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.List(ctx, &list); err != nil {
		return claims
	}
	seen := map[types.NamespacedName]struct{}{
		claims[0].NamespacedName: {},
	}
	for i := range list.Items {
		key := types.NamespacedName{Namespace: list.Items[i].Namespace, Name: list.Items[i].Name}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		claims = append(claims, reconcile.Request{NamespacedName: key})
	}
	return claims
}

func (r *SegmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.Reader == nil {
		r.Reader = mgr.GetAPIReader()
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("span")
	}
	// One watch, and it wakes every segment. A second For() would reconcile the
	// same object twice and the status writes would collide.
	// A delete does not bump generation, so the predicate has to notice it.
	return ctrl.NewControllerManagedBy(mgr).
		Named("segment").
		Watches(
			&spanv1alpha1.Segment{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueAll),
			builder.WithPredicates(segmentPredicate{}),
		).
		Complete(r)
}

type segmentPredicate struct{}

func (segmentPredicate) Create(event.CreateEvent) bool { return true }
func (segmentPredicate) Delete(event.DeleteEvent) bool { return true }
func (segmentPredicate) Generic(event.GenericEvent) bool {
	return false
}
func (segmentPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil || e.ObjectNew == nil {
		return false
	}
	if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
		return true
	}
	return e.ObjectOld.GetDeletionTimestamp().IsZero() && !e.ObjectNew.GetDeletionTimestamp().IsZero()
}

func specOf(seg *spanv1alpha1.Segment) planner.Spec {
	return planner.Spec{
		CIDR:    seg.Spec.CIDR,
		Gateway: seg.Spec.Gateway,
		Cluster: seg.Spec.Cluster,
		VLAN:    seg.Spec.VLAN,
		Link:    seg.Spec.Link,
	}
}

func claimOf(seg *spanv1alpha1.Segment) planner.Claim {
	return planner.Claim{
		UID:       string(seg.UID),
		Namespace: seg.Namespace,
		Name:      seg.Name,
		CIDR:      seg.Spec.CIDR,
		Created:   seg.CreationTimestamp.Time,
		Deleting:  !seg.DeletionTimestamp.IsZero(),
	}
}

func apiOps(ops []planner.Op) []spanv1alpha1.NetlinkOp {
	if len(ops) == 0 {
		return nil
	}
	out := make([]spanv1alpha1.NetlinkOp, len(ops))
	for i, op := range ops {
		out[i] = spanv1alpha1.NetlinkOp{Action: op.Action, Address: op.Address, VLAN: op.VLAN}
	}
	return out
}

func helperRequest(link string, ops []spanv1alpha1.NetlinkOp) helper.Request {
	out := make([]helper.Op, 0, len(ops))
	for _, op := range ops {
		out = append(out, helper.Op{Action: op.Action, Address: op.Address, VLAN: op.VLAN})
	}
	return helper.Request{Link: link, Ops: out}
}

func apiObserved(obs []helper.Observation) []spanv1alpha1.HostObservation {
	if len(obs) == 0 {
		return nil
	}
	out := make([]spanv1alpha1.HostObservation, len(obs))
	for i, item := range obs {
		out[i] = spanv1alpha1.HostObservation{
			Endpoint:  item.Endpoint,
			Link:      item.Link,
			Addresses: item.Addresses,
			Routes:    item.Routes,
			Error:     item.Error,
		}
	}
	return out
}

func linkOf(seg *spanv1alpha1.Segment) string {
	if seg.Spec.Link != "" {
		return seg.Spec.Link
	}
	return planner.DefaultLink
}

func programmedLinks(seg *spanv1alpha1.Segment) []string {
	var links []string
	seen := map[string]struct{}{}
	for _, obs := range seg.Status.Observed {
		if obs.Link == "" {
			continue
		}
		if _, ok := seen[obs.Link]; ok {
			continue
		}
		seen[obs.Link] = struct{}{}
		links = append(links, obs.Link)
	}
	return links
}

func setReady(seg *spanv1alpha1.Segment, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&seg.Status.Conditions, metav1.Condition{
		Type:               spanv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: seg.Generation,
	})
}

func setApplied(seg *spanv1alpha1.Segment, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&seg.Status.Conditions, metav1.Condition{
		Type:               spanv1alpha1.ConditionApplied,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: seg.Generation,
	})
}
