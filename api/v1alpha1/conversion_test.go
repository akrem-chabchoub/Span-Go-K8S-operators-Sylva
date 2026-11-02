package v1alpha1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"

	spanv1 "github.com/akrem/span/api/v1"
)

func TestConvertDefaultsLink(t *testing.T) {
	vlan := int32(100)
	src := &Segment{}
	src.Name = "lab"
	src.Namespace = "default"
	src.Spec = SegmentSpec{CIDR: "10.20.30.0/24", Gateway: "10.20.30.1", Cluster: "lab", VLAN: &vlan}
	src.Status.ConnectedRoute = "10.20.30.0/24"
	src.Status.Plan = []NetlinkOp{{Action: ActionAddAddress, Address: "10.20.30.1/24"}}

	var hub spanv1.Segment
	if err := src.ConvertTo(&hub); err != nil {
		t.Fatal(err)
	}
	if hub.Spec.Link != defaultLink || hub.Spec.CIDR != src.Spec.CIDR || hub.Spec.Gateway != src.Spec.Gateway {
		t.Fatalf("hub spec %#v", hub.Spec)
	}
	if hub.Spec.VLAN == nil || *hub.Spec.VLAN != 100 || hub.Status.Plan[0].Address != "10.20.30.1/24" {
		t.Fatalf("hub %#v", hub)
	}

	var back Segment
	if err := back.ConvertFrom(&hub); err != nil {
		t.Fatal(err)
	}
	if back.Spec.CIDR != src.Spec.CIDR || back.Spec.Link != defaultLink || back.Status.ConnectedRoute != src.Status.ConnectedRoute {
		t.Fatalf("round trip %#v", back)
	}
}

func TestConversionWebhookReadsOldObject(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(AddToScheme(scheme))
	utilruntime.Must(spanv1.AddToScheme(scheme))
	handler := conversion.NewWebhookHandler(scheme)

	body := `{
	  "apiVersion":"apiextensions.k8s.io/v1",
	  "kind":"ConversionReview",
	  "request":{
	    "uid":"lab-1",
	    "desiredAPIVersion":"span.dev/v1",
	    "objects":[{
	      "apiVersion":"span.dev/v1alpha1",
	      "kind":"Segment",
	      "metadata":{"name":"lab","namespace":"default"},
	      "spec":{"cidr":"10.20.30.0/24","gateway":"10.20.30.1","cluster":"lab"}
	    }]
	  }
	}`
	req := httptest.NewRequest(http.MethodPost, "/convert", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var review struct {
		Response struct {
			UID     string                  `json:"uid"`
			Result  struct{ Status string } `json:"result"`
			Objects []struct {
				APIVersion string `json:"apiVersion"`
				Spec       struct {
					CIDR string `json:"cidr"`
					Link string `json:"link"`
				} `json:"spec"`
			} `json:"convertedObjects"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	if review.Response.Result.Status != "Success" || review.Response.UID != "lab-1" {
		t.Fatalf("response %s", rec.Body.String())
	}
	if len(review.Response.Objects) != 1 {
		t.Fatalf("objects %s", rec.Body.String())
	}
	got := review.Response.Objects[0]
	if got.APIVersion != "span.dev/v1" || got.Spec.CIDR != "10.20.30.0/24" || got.Spec.Link != defaultLink {
		t.Fatalf("converted %#v", got)
	}
}
