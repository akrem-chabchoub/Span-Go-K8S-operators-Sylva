package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Segment is the stable view of a prefix promise.
// Storage stays v1alpha1. Reading v1 runs conversion.
type Segment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SegmentSpec   `json:"spec,omitempty"`
	Status SegmentStatus `json:"status,omitempty"`
}

func (*Segment) Hub() {}

type SegmentSpec struct {
	CIDR    string `json:"cidr"`
	Gateway string `json:"gateway"`
	Cluster string `json:"cluster"`
	VLAN    *int32 `json:"vlan,omitempty"`
	Link    string `json:"link,omitempty"`
}

type SegmentStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	Plan               []NetlinkOp        `json:"plan,omitempty"`
	ConnectedRoute     string             `json:"connectedRoute,omitempty"`
	Observed           []HostObservation  `json:"observed,omitempty"`
}

type NetlinkOp struct {
	Action  string `json:"action"`
	Address string `json:"address,omitempty"`
	VLAN    int32  `json:"vlan,omitempty"`
}

type HostObservation struct {
	Endpoint  string   `json:"endpoint"`
	Link      string   `json:"link,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Routes    []string `json:"routes,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type SegmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Segment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Segment{}, &SegmentList{})
}
