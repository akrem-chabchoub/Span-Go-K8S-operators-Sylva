package v1alpha1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	spanv1 "github.com/akrem/span/api/v1"
)

const defaultLink = "span0"

func (in *Segment) ConvertTo(dst conversion.Hub) error {
	hub, ok := dst.(*spanv1.Segment)
	if !ok {
		return fmt.Errorf("expected *v1.Segment, got %T", dst)
	}
	hub.ObjectMeta = *in.ObjectMeta.DeepCopy()
	hub.Spec = spanv1.SegmentSpec{
		CIDR:    in.Spec.CIDR,
		Gateway: in.Spec.Gateway,
		Cluster: in.Spec.Cluster,
		VLAN:    copyVLAN(in.Spec.VLAN),
		Link:    in.Spec.Link,
	}
	if hub.Spec.Link == "" {
		hub.Spec.Link = defaultLink
	}
	hub.Status = toV1Status(in.Status)
	return nil
}

func (in *Segment) ConvertFrom(src conversion.Hub) error {
	hub, ok := src.(*spanv1.Segment)
	if !ok {
		return fmt.Errorf("expected *v1.Segment, got %T", src)
	}
	in.ObjectMeta = *hub.ObjectMeta.DeepCopy()
	in.Spec = SegmentSpec{
		CIDR:    hub.Spec.CIDR,
		Gateway: hub.Spec.Gateway,
		Cluster: hub.Spec.Cluster,
		VLAN:    copyVLAN(hub.Spec.VLAN),
		Link:    hub.Spec.Link,
	}
	in.Status = fromV1Status(hub.Status)
	return nil
}

func copyVLAN(in *int32) *int32 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func toV1Status(in SegmentStatus) spanv1.SegmentStatus {
	out := spanv1.SegmentStatus{
		ObservedGeneration: in.ObservedGeneration,
		ConnectedRoute:     in.ConnectedRoute,
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	for _, op := range in.Plan {
		out.Plan = append(out.Plan, spanv1.NetlinkOp{Action: op.Action, Address: op.Address, VLAN: op.VLAN})
	}
	for _, obs := range in.Observed {
		out.Observed = append(out.Observed, spanv1.HostObservation{
			Endpoint:  obs.Endpoint,
			Link:      obs.Link,
			Addresses: append([]string(nil), obs.Addresses...),
			Routes:    append([]string(nil), obs.Routes...),
			Error:     obs.Error,
		})
	}
	return out
}

func fromV1Status(in spanv1.SegmentStatus) SegmentStatus {
	out := SegmentStatus{
		ObservedGeneration: in.ObservedGeneration,
		ConnectedRoute:     in.ConnectedRoute,
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	for _, op := range in.Plan {
		out.Plan = append(out.Plan, NetlinkOp{Action: op.Action, Address: op.Address, VLAN: op.VLAN})
	}
	for _, obs := range in.Observed {
		out.Observed = append(out.Observed, HostObservation{
			Endpoint:  obs.Endpoint,
			Link:      obs.Link,
			Addresses: append([]string(nil), obs.Addresses...),
			Routes:    append([]string(nil), obs.Routes...),
			Error:     obs.Error,
		})
	}
	return out
}
