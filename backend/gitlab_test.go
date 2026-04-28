package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchRunnersGraphQL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("missing PRIVATE-TOKEN header")
		}
		if r.URL.Path != "/api/graphql" {
			t.Errorf("path=%s, want /api/graphql", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"data": {
				"runners": {
					"nodes": [
						{
							"id": "gid://gitlab/Ci::Runner/42",
							"description": "shared-runner-1",
							"status": "ONLINE",
							"paused": false,
							"runnerType": "INSTANCE_TYPE",
							"tagList": ["docker", "linux"],
							"contactedAt": "2026-01-01T12:00:00Z"
						},
						{
							"id": "gid://gitlab/Ci::Runner/99",
							"description": "project-runner",
							"status": "OFFLINE",
							"paused": true,
							"runnerType": "PROJECT_TYPE",
							"tagList": null,
							"contactedAt": null
						}
					]
				}
			}
		}`))
	}))
	defer srv.Close()

	gl := newGitLabClient(srv.URL, "test-token")
	out := fetchRunnersGraphQL(context.Background(), gl)
	if len(out) != 2 {
		t.Fatalf("got %d runners, want 2", len(out))
	}

	r1 := out[0]
	if r1.ID != 42 {
		t.Errorf("ID=%d, want 42 (extracted from gid://gitlab/Ci::Runner/42)", r1.ID)
	}
	if r1.Status != "online" {
		t.Errorf("Status=%q, want %q (lowercased)", r1.Status, "online")
	}
	if r1.RunnerType != "instance_type" {
		t.Errorf("RunnerType=%q, want instance_type", r1.RunnerType)
	}
	if !r1.IsShared {
		t.Errorf("IsShared=%v, want true (instance_type)", r1.IsShared)
	}
	if len(r1.Tags) != 2 || r1.Tags[0] != "docker" {
		t.Errorf("Tags=%v, want [docker linux]", r1.Tags)
	}

	r2 := out[1]
	if r2.IsShared {
		t.Errorf("project_type runner: IsShared=true, want false")
	}
	if r2.Tags == nil {
		t.Error("nil Tags should be normalized to []string{} for stable JSON")
	}
}

func TestFetchRunnersGraphQLErrorsField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"resource access denied"}]}`))
	}))
	defer srv.Close()

	gl := newGitLabClient(srv.URL, "tok")
	if out := fetchRunnersGraphQL(context.Background(), gl); out != nil {
		t.Errorf("graphql errors{} should produce nil (signal to fallback), got %v", out)
	}
	// побочный эффект: graphQLDisabled должен выставиться
	runnersCache.mu.Lock()
	disabled := runnersCache.graphQLDisabled
	runnersCache.graphQLDisabled = false // сброс для других тестов
	runnersCache.mu.Unlock()
	if !disabled {
		t.Error("graphQLDisabled should be set after errors response")
	}
}

func TestFetchRunnersREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v4/runners") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`[
			{"id":1,"description":"runner-1","status":"online","paused":false,
			 "is_shared":true,"runner_type":"instance_type"}
		]`))
	}))
	defer srv.Close()

	gl := newGitLabClient(srv.URL, "tok")
	out := fetchRunnersREST(context.Background(), gl)
	if len(out) != 1 || out[0].ID != 1 || out[0].Status != "online" {
		t.Errorf("got %+v, want [{ID:1 status:online ...}]", out)
	}
	if out[0].Tags == nil {
		t.Error("Tags should be []string{}, not nil")
	}
}

func TestEnrichRunnersDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"contacted_at": "2026-04-01T10:00:00Z",
			"tag_list": ["docker","linux"],
			"executor_type": "docker",
			"name": "runner-host-1",
			"web_url": "https://gitlab.example.com/admin/runners/42"
		}`))
	}))
	defer srv.Close()

	runners := []RunnerItem{{ID: 42}}
	gl := newGitLabClient(srv.URL, "tok")
	enrichRunnersDetail(context.Background(), gl, runners)

	if runners[0].ContactedAt != "2026-04-01T10:00:00Z" {
		t.Errorf("ContactedAt=%q", runners[0].ContactedAt)
	}
	if runners[0].Executor != "docker" {
		t.Errorf("Executor=%q", runners[0].Executor)
	}
	if runners[0].Hostname != "runner-host-1" {
		t.Errorf("Hostname=%q", runners[0].Hostname)
	}
	if runners[0].WebURL != "https://gitlab.example.com/admin/runners/42" {
		t.Errorf("WebURL=%q", runners[0].WebURL)
	}
}

func TestRunnersCacheTTL(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/api/graphql" {
			w.Write([]byte(`{"data":{"runners":{"nodes":[]}}}`))
		} else {
			w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	// Сбросим кеш, чтобы не протекал из других тестов.
	runnersCache.mu.Lock()
	runnersCache.payload = nil
	runnersCache.expires = time.Time{}
	runnersCache.graphQLDisabled = false
	runnersCache.mu.Unlock()

	gl := newGitLabClient(srv.URL, "tok")
	handler := handleRunners(gl)

	// Первый вызов идёт по сети.
	w1 := httptest.NewRecorder()
	handler(w1, mustReq(t, "/api/runners"))
	first := hits

	// Второй вызов в течение TTL — берётся из кеша, GitLab не дёргается.
	w2 := httptest.NewRecorder()
	handler(w2, mustReq(t, "/api/runners"))
	if hits != first {
		t.Errorf("cache miss: hits went %d → %d during TTL window", first, hits)
	}
}
