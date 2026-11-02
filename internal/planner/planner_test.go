package planner

import (
	"strings"
	"testing"
	"time"
)

func TestPlanHappyPath(t *testing.T) {
	vlan := int32(100)
	got, errs := Plan(Spec{
		CIDR:    "10.20.30.0/24",
		Gateway: "10.20.30.1",
		Cluster: "lab",
		VLAN:    &vlan,
	})
	if len(errs) > 0 {
		t.Fatalf("plan: %v", errs.ToAggregate())
	}
	if got.Identity != "10.20.30.0/24|10.20.30.1|lab" {
		t.Fatalf("identity %q", got.Identity)
	}
	if got.ConnectedRoute != "10.20.30.0/24" {
		t.Fatalf("route %q", got.ConnectedRoute)
	}
	if len(got.Ops) != 2 {
		t.Fatalf("ops %#v", got.Ops)
	}
	if got.Ops[0].Action != ActionAddVLAN || got.Ops[0].VLAN != 100 {
		t.Fatalf("vlan op %#v", got.Ops[0])
	}
	if got.Ops[1].Action != ActionAddAddress || got.Ops[1].Address != "10.20.30.1/24" {
		t.Fatalf("address op %#v", got.Ops[1])
	}
	if got.Link != DefaultLink {
		t.Fatalf("link %q", got.Link)
	}
}

func TestPlanUsesNamedLink(t *testing.T) {
	got, errs := Plan(Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.1", Cluster: "lab", Link: "span1"})
	if len(errs) > 0 {
		t.Fatal(errs.ToAggregate())
	}
	if got.Link != "span1" {
		t.Fatalf("link %q", got.Link)
	}
	if got.Identity != "10.20.30.0/24|10.20.30.1|lab" {
		t.Fatalf("link leaked into identity %q", got.Identity)
	}
}

func TestPlanIPv6(t *testing.T) {
	got, errs := Plan(Spec{CIDR: "2001:db8:1::/64", Gateway: "2001:db8:1::1", Cluster: "lab"})
	if len(errs) > 0 {
		t.Fatalf("plan: %v", errs.ToAggregate())
	}
	if got.Ops[0].Address != "2001:db8:1::1/64" {
		t.Fatalf("address %q", got.Ops[0].Address)
	}
}

func TestValidateRejects(t *testing.T) {
	vlanBad := int32(0)
	vlanHigh := int32(4095)
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{name: "not a cidr", spec: Spec{CIDR: "nope", Gateway: "10.0.0.1", Cluster: "lab"}, want: "must be a prefix"},
		{name: "host bits", spec: Spec{CIDR: "10.20.30.1/24", Gateway: "10.20.30.1", Cluster: "lab"}, want: "network address"},
		{name: "too big", spec: Spec{CIDR: "10.0.0.0/7", Gateway: "10.0.0.1", Cluster: "lab"}, want: "IPv4 prefix"},
		{name: "outside", spec: Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.31.1", Cluster: "lab"}, want: "inside cidr"},
		{name: "network address", spec: Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.0", Cluster: "lab"}, want: "network address"},
		{name: "broadcast", spec: Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.255", Cluster: "lab"}, want: "broadcast"},
		{name: "cluster", spec: Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.1", Cluster: "Lab"}, want: "DNS label"},
		{name: "vlan zero", spec: Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.1", Cluster: "lab", VLAN: &vlanBad}, want: "1 and 4094"},
		{name: "vlan high", spec: Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.1", Cluster: "lab", VLAN: &vlanHigh}, want: "1 and 4094"},
		{name: "leading zero", spec: Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.001", Cluster: "lab"}, want: "IP address"},
		{name: "bad link", spec: Spec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.1", Cluster: "lab", Link: "1bad"}, want: "letter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := Validate(tc.spec)
			if len(errs) == 0 {
				t.Fatal("expected an error")
			}
			msg := errs.ToAggregate().Error()
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("error %q does not contain %q", msg, tc.want)
			}
		})
	}
}

func TestPointToPointAllowsBothEnds(t *testing.T) {
	for _, spec := range []Spec{
		{CIDR: "10.20.30.0/31", Gateway: "10.20.30.0", Cluster: "lab"},
		{CIDR: "10.20.30.0/31", Gateway: "10.20.30.1", Cluster: "lab"},
		{CIDR: "2001:db8:1::/127", Gateway: "2001:db8:1::", Cluster: "lab"},
		{CIDR: "2001:db8:1::/127", Gateway: "2001:db8:1::1", Cluster: "lab"},
	} {
		if errs := Validate(spec); len(errs) > 0 {
			t.Fatalf("%#v: %v", spec, errs.ToAggregate())
		}
	}
}

func TestCIDRsOverlap(t *testing.T) {
	yes, err := CIDRsOverlap("10.51.0.0/16", "10.51.8.0/24")
	if err != nil || !yes {
		t.Fatalf("contained overlap: %v %v", yes, err)
	}
	no, err := CIDRsOverlap("10.51.0.0/24", "10.51.1.0/24")
	if err != nil || no {
		t.Fatalf("adjacent: %v %v", no, err)
	}
	_, err = CIDRsOverlap("nope", "10.0.0.0/24")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestBlockedBy(t *testing.T) {
	older := Claim{UID: "a", Namespace: "campus", Name: "older", CIDR: "10.51.0.0/16", Created: time.Unix(1, 0)}
	newer := Claim{UID: "b", Namespace: "campus", Name: "newer", CIDR: "10.51.8.0/24", Created: time.Unix(2, 0)}

	if _, blocked := BlockedBy(older, []Claim{newer}, false); blocked {
		t.Fatal("older claim should keep the prefix")
	}
	blocker, blocked := BlockedBy(newer, []Claim{older}, false)
	if !blocked || blocker.Name != "older" {
		t.Fatalf("newer should lose to older, got %#v %v", blocker, blocked)
	}

	deleting := older
	deleting.Deleting = true
	blocker, blocked = BlockedBy(newer, []Claim{deleting}, false)
	if !blocked || blocker.Name != "older" {
		t.Fatal("a deleting claim holds the prefix until the address is gone")
	}

	if _, blocked := BlockedBy(newer, []Claim{older}, true); !blocked {
		t.Fatal("a new object loses to anything already stored")
	}

	tieA := Claim{UID: "1", Namespace: "a", Name: "same", CIDR: "10.0.0.0/8", Created: time.Unix(5, 0)}
	tieB := Claim{UID: "2", Namespace: "b", Name: "same", CIDR: "10.1.0.0/16", Created: time.Unix(5, 0)}
	if _, blocked := BlockedBy(tieA, []Claim{tieB}, false); blocked {
		t.Fatal("lower namespace/name should win a clock tie")
	}
	if _, blocked := BlockedBy(tieB, []Claim{tieA}, false); !blocked {
		t.Fatal("higher namespace/name should lose a clock tie")
	}

	self := Claim{Namespace: "campus", Name: "alone", CIDR: "10.9.0.0/24", Created: time.Unix(3, 0)}
	if _, blocked := BlockedBy(self, []Claim{self}, false); blocked {
		t.Fatal("a segment must not lose its prefix to itself")
	}
}
