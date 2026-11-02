package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *Segment) DeepCopyInto(out *Segment) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *Segment) DeepCopy() *Segment {
	if in == nil {
		return nil
	}
	out := new(Segment)
	in.DeepCopyInto(out)
	return out
}

func (in *Segment) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	return in.DeepCopy()
}

func (in *SegmentList) DeepCopyInto(out *SegmentList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Segment, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *SegmentList) DeepCopy() *SegmentList {
	if in == nil {
		return nil
	}
	out := new(SegmentList)
	in.DeepCopyInto(out)
	return out
}

func (in *SegmentList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	return in.DeepCopy()
}

func (in *SegmentSpec) DeepCopyInto(out *SegmentSpec) {
	*out = *in
	if in.VLAN != nil {
		v := *in.VLAN
		out.VLAN = &v
	}
}

func (in *SegmentSpec) DeepCopy() *SegmentSpec {
	if in == nil {
		return nil
	}
	out := new(SegmentSpec)
	in.DeepCopyInto(out)
	return out
}

func (in *SegmentStatus) DeepCopyInto(out *SegmentStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	if in.Plan != nil {
		out.Plan = make([]NetlinkOp, len(in.Plan))
		copy(out.Plan, in.Plan)
	}
	if in.Observed != nil {
		out.Observed = make([]HostObservation, len(in.Observed))
		for i := range in.Observed {
			in.Observed[i].DeepCopyInto(&out.Observed[i])
		}
	}
}

func (in *HostObservation) DeepCopyInto(out *HostObservation) {
	*out = *in
	if in.Addresses != nil {
		out.Addresses = append([]string(nil), in.Addresses...)
	}
	if in.Routes != nil {
		out.Routes = append([]string(nil), in.Routes...)
	}
}

func (in *SegmentStatus) DeepCopy() *SegmentStatus {
	if in == nil {
		return nil
	}
	out := new(SegmentStatus)
	in.DeepCopyInto(out)
	return out
}
