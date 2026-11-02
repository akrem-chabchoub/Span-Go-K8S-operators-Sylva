package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	Finalizer          = "span.dev/segment"
	IdentityAnnotation = "span.dev/identity"

	ConditionReady   = "Ready"
	ConditionApplied = "Applied"

	ReasonPlanned       = "Planned"
	ReasonOverlap       = "Overlap"
	ReasonInvalid       = "Invalid"
	ReasonImmutable     = "Immutable"
	ReasonPendingHelper = "PendingHelper"
	ReasonNotPlanned    = "NotPlanned"
	ReasonApplied       = "Applied"
	ReasonHelperFailed  = "HelperFailed"

	ActionAddVLAN    = "add-vlan"
	ActionAddAddress = "add-address"
)

type SegmentSpec struct {
	// CIDR is the prefix, written as a network address. Example: 10.20.30.0/24.
	CIDR string `json:"cidr"`

	// Gateway is the address span will put on a host. It must sit inside CIDR.
	Gateway string `json:"gateway"`

	// Cluster is the workload cluster this prefix belongs to.
	// A DNS label. It cannot change after the promise is stored.
	Cluster string `json:"cluster"`

	// VLAN is optional. Nil means the address is not tagged.
	// A pointer so "omitted" is not the same as a number.
	VLAN *int32 `json:"vlan,omitempty"`

	// Link is the device the helper programs. Empty means span0, a dummy
	// device inside the helper's own network namespace. It is not part of
	// the promise, so it can change without a delete.
	Link string `json:"link,omitempty"`
}

type NetlinkOp struct {
	Action  string `json:"action"`
	Address string `json:"address,omitempty"`
	VLAN    int32  `json:"vlan,omitempty"`
}

type SegmentStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Plan is what the helper will be asked to do. Empty means "do not touch a host."
	Plan []NetlinkOp `json:"plan,omitempty"`

	// ConnectedRoute is the on-link route the kernel creates once the address exists.
	// It is not a separate netlink operation.
	ConnectedRoute string `json:"connectedRoute,omitempty"`

	// Observed is what the helper read back from the kernel.
	Observed []HostObservation `json:"observed,omitempty"`
}

type HostObservation struct {
	Endpoint  string   `json:"endpoint"`
	Link      string   `json:"link,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Routes    []string `json:"routes,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=seg,scope=Namespaced
// +kubebuilder:printcolumn:name="CIDR",type=string,JSONPath=`.spec.cidr`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.cluster`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

type Segment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SegmentSpec   `json:"spec,omitempty"`
	Status SegmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type SegmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Segment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Segment{}, &SegmentList{})
}
