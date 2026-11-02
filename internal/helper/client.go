// Package helper talks to the Rust process that programs a network namespace.
package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

const defaultLimit = 4

type Op struct {
	Action      string `json:"action"`
	Address     string `json:"address,omitempty"`
	VLAN        int32  `json:"vlan,omitempty"`
	Destination string `json:"destination,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
}

type Request struct {
	Link string `json:"link,omitempty"`
	Ops  []Op   `json:"ops"`
}

type Report struct {
	Link      string   `json:"link"`
	Addresses []string `json:"addresses"`
	Routes    []string `json:"routes"`
	Notes     []string `json:"notes"`
}

type Observation struct {
	Endpoint  string
	Link      string
	Addresses []string
	Routes    []string
	Notes     []string
	Error     string
}

// Applier programs every configured helper and reads back what the kernel has.
type Applier interface {
	Apply(ctx context.Context, req Request) ([]Observation, error)
	Remove(ctx context.Context, req Request) ([]Observation, error)
}

// Pool calls each endpoint with a small concurrency limit.
// An empty endpoint list is an error. It is not a silent success.
type Pool struct {
	Endpoints []string
	Client    *http.Client
	Limit     int
}

func (p *Pool) Apply(ctx context.Context, req Request) ([]Observation, error) {
	return p.fanout(ctx, "/apply", req)
}

func (p *Pool) Remove(ctx context.Context, req Request) ([]Observation, error) {
	return p.fanout(ctx, "/remove", req)
}

func (p *Pool) fanout(ctx context.Context, path string, req Request) ([]Observation, error) {
	if len(p.Endpoints) == 0 {
		return nil, errors.New("no helper endpoints")
	}
	limit := p.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	httpClient := p.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}

	obs := make([]Observation, len(p.Endpoints))
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(limit)
	for i, endpoint := range p.Endpoints {
		group.Go(func() error {
			got, err := post(ctx, httpClient, strings.TrimRight(endpoint, "/"), path, req)
			obs[i] = got
			if err != nil {
				obs[i].Endpoint = endpoint
				obs[i].Error = err.Error()
				return err
			}
			return nil
		})
	}
	err := group.Wait()
	return obs, err
}

func post(ctx context.Context, httpClient *http.Client, endpoint, path string, req Request) (Observation, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Observation{Endpoint: endpoint}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return Observation{Endpoint: endpoint}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return Observation{Endpoint: endpoint}, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Observation{Endpoint: endpoint}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Observation{Endpoint: endpoint}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	var report Report
	if err := json.Unmarshal(payload, &report); err != nil {
		return Observation{Endpoint: endpoint}, fmt.Errorf("decode report: %w", err)
	}
	return Observation{
		Endpoint:  endpoint,
		Link:      report.Link,
		Addresses: report.Addresses,
		Routes:    report.Routes,
		Notes:     report.Notes,
	}, nil
}
