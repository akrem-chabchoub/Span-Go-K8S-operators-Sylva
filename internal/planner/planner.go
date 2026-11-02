// Package planner decides whether a segment promise is acceptable and which
// kernel operations that promise implies. It does not talk to a cluster.
package planner

import (
	"errors"
	"fmt"
	"net"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	ActionAddVLAN    = "add-vlan"
	ActionAddAddress = "add-address"

	// DefaultLink is the dummy device the lab helper creates in its own
	// network namespace. A real node would name a different device.
	DefaultLink = "span0"

	minIPv4Prefix = 8
	maxIPv4Prefix = 31
	minIPv6Prefix = 32
	maxIPv6Prefix = 127
)

type Spec struct {
	CIDR    string
	Gateway string
	Cluster string
	VLAN    *int32
	Link    string
}

type Op struct {
	Action  string
	Address string
	VLAN    int32
}

type Result struct {
	Ops            []Op
	ConnectedRoute string
	Identity       string
	Link           string
}

// Claim is the small slice of an object needed to decide who keeps a prefix.
type Claim struct {
	UID       string
	Namespace string
	Name      string
	CIDR      string
	Created   time.Time
	Deleting  bool
}

func Validate(spec Spec) field.ErrorList {
	var errs field.ErrorList
	base := field.NewPath("spec")

	network, cidrErr := parseNetwork(spec.CIDR)
	if cidrErr != nil {
		errs = append(errs, field.Invalid(base.Child("cidr"), spec.CIDR, cidrErr.Error()))
	}

	gw := net.ParseIP(spec.Gateway)
	switch {
	case gw == nil:
		errs = append(errs, field.Invalid(base.Child("gateway"), spec.Gateway, "must be an IP address"))
	case network != nil && !network.Contains(gw):
		errs = append(errs, field.Invalid(base.Child("gateway"), spec.Gateway, "must sit inside cidr"))
	case network != nil:
		if msg := gatewayUsable(network, gw); msg != "" {
			errs = append(errs, field.Invalid(base.Child("gateway"), spec.Gateway, msg))
		}
	}

	if msgs := validation.IsDNS1123Label(spec.Cluster); len(msgs) > 0 {
		errs = append(errs, field.Invalid(base.Child("cluster"), spec.Cluster, "must be a DNS label: "+msgs[0]))
	}

	if spec.VLAN != nil {
		v := *spec.VLAN
		if v < 1 || v > 4094 {
			errs = append(errs, field.Invalid(base.Child("vlan"), v, "must be between 1 and 4094"))
		}
	}
	if spec.Link != "" {
		if msg := linkName(spec.Link); msg != "" {
			errs = append(errs, field.Invalid(base.Child("link"), spec.Link, msg))
		}
	}
	return errs
}

func Plan(spec Spec) (Result, field.ErrorList) {
	if errs := Validate(spec); len(errs) > 0 {
		return Result{}, errs
	}
	_, network, err := net.ParseCIDR(spec.CIDR)
	if err != nil {
		return Result{}, field.ErrorList{field.Invalid(field.NewPath("spec", "cidr"), spec.CIDR, err.Error())}
	}
	gw := net.ParseIP(spec.Gateway)
	ones, _ := network.Mask.Size()

	var ops []Op
	if spec.VLAN != nil {
		ops = append(ops, Op{Action: ActionAddVLAN, VLAN: *spec.VLAN})
	}
	ops = append(ops, Op{
		Action:  ActionAddAddress,
		Address: fmt.Sprintf("%s/%d", gw.String(), ones),
	})
	link := spec.Link
	if link == "" {
		link = DefaultLink
	}
	return Result{
		Ops:            ops,
		ConnectedRoute: network.String(),
		Identity:       fmt.Sprintf("%s|%s|%s", network.String(), gw.String(), spec.Cluster),
		Link:           link,
	}, nil
}

func CIDRsOverlap(a, b string) (bool, error) {
	_, na, err := net.ParseCIDR(a)
	if err != nil {
		return false, fmt.Errorf("cidr %q: %w", a, err)
	}
	_, nb, err := net.ParseCIDR(b)
	if err != nil {
		return false, fmt.Errorf("cidr %q: %w", b, err)
	}
	return na.Contains(nb.IP) || nb.Contains(na.IP), nil
}

// BlockedBy returns another claim that should keep the prefix.
// selfIsNew is true during admission of an object that is not stored yet.
// A zero creation time would otherwise look older than every real object.
//
// A deleting claim still holds the prefix. The address can be on the host
// until the helper finishes removing it.
func BlockedBy(self Claim, others []Claim, selfIsNew bool) (Claim, bool) {
	var (
		found Claim
		ok    bool
	)
	for _, other := range others {
		if other.Namespace == self.Namespace && other.Name == self.Name {
			continue
		}
		if other.UID != "" && other.UID == self.UID {
			continue
		}
		overlap, err := CIDRsOverlap(self.CIDR, other.CIDR)
		if err != nil || !overlap {
			continue
		}
		if selfIsNew || !wins(self, other) {
			if !ok || wins(other, found) {
				found = other
				ok = true
			}
		}
	}
	return found, ok
}

func wins(a, b Claim) bool {
	if a.Created.Before(b.Created) {
		return true
	}
	if b.Created.Before(a.Created) {
		return false
	}
	ak := a.Namespace + "/" + a.Name
	bk := b.Namespace + "/" + b.Name
	if ak != bk {
		return ak < bk
	}
	return a.UID < b.UID
}

func parseNetwork(cidr string) (*net.IPNet, error) {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, errors.New("must be a prefix, for example 10.20.30.0/24")
	}
	if !ip.Equal(network.IP) {
		return nil, errors.New("must be the network address, with the host bits cleared")
	}
	if msg := prefixSize(network); msg != "" {
		return nil, errors.New(msg)
	}
	return network, nil
}

func prefixSize(network *net.IPNet) string {
	ones, bits := network.Mask.Size()
	switch bits {
	case 32:
		if ones < minIPv4Prefix || ones > maxIPv4Prefix {
			return fmt.Sprintf("IPv4 prefix must be /%d through /%d", minIPv4Prefix, maxIPv4Prefix)
		}
	case 128:
		if ones < minIPv6Prefix || ones > maxIPv6Prefix {
			return fmt.Sprintf("IPv6 prefix must be /%d through /%d", minIPv6Prefix, maxIPv6Prefix)
		}
	default:
		return "prefix length is not IPv4 or IPv6"
	}
	return ""
}

func gatewayUsable(network *net.IPNet, gw net.IP) string {
	ones, bits := network.Mask.Size()
	// /31 and /127 are point-to-point. Both addresses are hosts.
	if bits == 32 && ones >= 31 {
		return ""
	}
	if bits == 128 && ones >= 127 {
		return ""
	}
	if gw.Equal(network.IP) {
		return "must be a host address, not the network address"
	}
	if bits == 32 && gw.Equal(broadcast(network)) {
		return "must be a host address, not the broadcast address"
	}
	return ""
}

func linkName(name string) string {
	if len(name) > 15 {
		return "must be 15 characters or fewer"
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
		if i == 0 {
			ok = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		}
		if !ok {
			return "must start with a letter and use only letters, digits, '.', '_' or '-'"
		}
	}
	return ""
}

func broadcast(network *net.IPNet) net.IP {
	ip := network.IP.To4()
	mask := net.IP(network.Mask).To4()
	if ip == nil || mask == nil {
		return nil
	}
	out := make(net.IP, len(ip))
	for i := range ip {
		out[i] = ip[i] | ^mask[i]
	}
	return out
}
