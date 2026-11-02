package helper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestPoolEmptyEndpoints(t *testing.T) {
	_, err := (&Pool{}).Apply(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "no helper endpoints") {
		t.Fatalf("got %v", err)
	}
}

func TestPoolFanout(t *testing.T) {
	var mu sync.Mutex
	seen := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apply" {
			t.Errorf("path %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Error(err)
		}
		if req.Link != "span0" || len(req.Ops) != 1 || req.Ops[0].Address != "10.20.30.1/24" {
			t.Errorf("request %#v", req)
		}
		mu.Lock()
		seen++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"link":"span0","addresses":["10.20.30.1/24"],"routes":["10.20.30.0/24"],"notes":[]}`))
	}
	one := httptest.NewServer(http.HandlerFunc(handler))
	two := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(one.Close)
	t.Cleanup(two.Close)

	obs, err := (&Pool{Endpoints: []string{one.URL, two.URL}, Limit: 2}).Apply(context.Background(), Request{
		Link: "span0",
		Ops:  []Op{{Action: "add-address", Address: "10.20.30.1/24"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 2 || len(obs) != 2 {
		t.Fatalf("seen %d obs %#v", seen, obs)
	}
	for _, item := range obs {
		if len(item.Addresses) != 1 || item.Addresses[0] != "10.20.30.1/24" || item.Link != "span0" {
			t.Fatalf("observation %#v", item)
		}
	}
}

func TestPoolReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "link is down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	obs, err := (&Pool{Endpoints: []string{srv.URL}}).Remove(context.Background(), Request{Link: "span0"})
	if err == nil || !strings.Contains(err.Error(), "link is down") {
		t.Fatalf("got %v", err)
	}
	if len(obs) != 1 || obs[0].Error == "" {
		t.Fatalf("observation %#v", obs)
	}
}
