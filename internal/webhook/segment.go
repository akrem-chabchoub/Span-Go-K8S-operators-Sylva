package webhook

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	spanv1alpha1 "github.com/akrem/span/api/v1alpha1"
	"github.com/akrem/span/internal/planner"
)

const validatePath = "/validate-span-dev-v1alpha1-segment"

type SegmentValidator struct {
	client.Reader
}

var _ admission.CustomValidator = (*SegmentValidator)(nil)

func (v *SegmentValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	seg, err := asSegment(obj)
	if err != nil {
		return nil, err
	}
	if err := v.validate(ctx, nil, seg, true); err != nil {
		return nil, err
	}
	return nil, nil
}

func (v *SegmentValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldSeg, err := asSegment(oldObj)
	if err != nil {
		return nil, err
	}
	newSeg, err := asSegment(newObj)
	if err != nil {
		return nil, err
	}
	if err := v.validate(ctx, oldSeg, newSeg, false); err != nil {
		return nil, err
	}
	return nil, nil
}

func (v *SegmentValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *SegmentValidator) SetupWithManager(mgr ctrl.Manager) error {
	if v.Reader == nil {
		v.Reader = mgr.GetAPIReader()
	}
	mgr.GetWebhookServer().Register(validatePath, admission.WithCustomValidator(mgr.GetScheme(), &spanv1alpha1.Segment{}, v))
	return nil
}

func (v *SegmentValidator) validate(ctx context.Context, oldSeg, seg *spanv1alpha1.Segment, isNew bool) error {
	if oldSeg != nil {
		if errs := immutable(oldSeg, seg); len(errs) > 0 {
			return deny(seg, errs)
		}
	}
	if errs := planner.Validate(specOf(seg)); len(errs) > 0 {
		return deny(seg, errs)
	}
	claims, err := v.claims(ctx)
	if err != nil {
		return fmt.Errorf("list segments: %w", err)
	}
	if blocker, blocked := planner.BlockedBy(claimOf(seg), claims, isNew); blocked {
		msg := fmt.Sprintf("overlaps %s/%s (%s), which keeps the prefix", blocker.Namespace, blocker.Name, blocker.CIDR)
		return deny(seg, field.ErrorList{
			field.Invalid(field.NewPath("spec").Child("cidr"), seg.Spec.CIDR, msg),
		})
	}
	return nil
}

func (v *SegmentValidator) claims(ctx context.Context) ([]planner.Claim, error) {
	var list spanv1alpha1.SegmentList
	if err := v.List(ctx, &list); err != nil {
		return nil, err
	}
	out := make([]planner.Claim, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, claimOf(&list.Items[i]))
	}
	return out, nil
}

func immutable(oldSeg, newSeg *spanv1alpha1.Segment) field.ErrorList {
	oldPlan, oldErrs := planner.Plan(specOf(oldSeg))
	if len(oldErrs) > 0 {
		return nil
	}
	newPlan, newErrs := planner.Plan(specOf(newSeg))
	if len(newErrs) > 0 {
		return nil
	}
	if oldPlan.Identity == newPlan.Identity {
		return nil
	}
	return field.ErrorList{
		field.Forbidden(field.NewPath("spec"), "cidr, gateway, and cluster cannot change; delete the segment and create a new one"),
	}
}

func deny(seg *spanv1alpha1.Segment, errs field.ErrorList) error {
	return apierrors.NewInvalid(schema.GroupKind{Group: spanv1alpha1.Group, Kind: "Segment"}, seg.Name, errs)
}

func asSegment(obj runtime.Object) (*spanv1alpha1.Segment, error) {
	seg, ok := obj.(*spanv1alpha1.Segment)
	if !ok {
		return nil, fmt.Errorf("expected a Segment, got %T", obj)
	}
	return seg, nil
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
	deleting := seg.DeletionTimestamp != nil && !seg.DeletionTimestamp.IsZero()
	return planner.Claim{
		UID:       string(seg.UID),
		Namespace: seg.Namespace,
		Name:      seg.Name,
		CIDR:      seg.Spec.CIDR,
		Created:   seg.CreationTimestamp.Time,
		Deleting:  deleting,
	}
}
