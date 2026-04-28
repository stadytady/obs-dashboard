package main

import (
	"fmt"
	"net/http"
	"time"
)

var startedAt = time.Now()

type AboutInfo struct {
	App       string            `json:"app"`
	Uptime    string            `json:"uptime"`
	Stub      bool              `json:"stub"`
	Config    map[string]string `json:"config"`
	Endpoints []AboutEndpoint   `json:"endpoints"`
}

type AboutEndpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Desc   string `json:"desc"`
	Group  string `json:"group"`
}

func handleAbout(w http.ResponseWriter, _ *http.Request) {
	up := time.Since(startedAt).Round(time.Second)

	cfgMap := map[string]string{
		"addr":   cfg.Addr,
		"static": cfg.StaticDir,
		"stub":   fmt.Sprintf("%v", cfg.Stub),
	}
	if cfg.ClustersFile != "" {
		cfgMap["clusters"] = cfg.ClustersFile
	} else {
		cfgMap["kubeconfig"] = cfg.Kubeconfig
	}
	if cfg.ServersFile != "" {
		cfgMap["servers"] = cfg.ServersFile
	}
	if cfg.GitLabURL != "" {
		cfgMap["gitlab"] = cfg.GitLabURL
	}
	if cfg.GrafanaURL != "" {
		cfgMap["grafana"] = cfg.GrafanaURL
	}
	if cfg.OpenSearchURL != "" {
		cfgMap["opensearch"] = cfg.OpenSearchURL
	}
	if cfg.JaegerURL != "" {
		cfgMap["jaeger"] = cfg.JaegerURL
	}

	endpoints := []AboutEndpoint{
		{"GET", "/api/clusters", "cluster list + per-cluster health", "Kubernetes"},
		{"GET", "/api/health", "k8s + metrics-server reachability", "Kubernetes"},
		{"GET", "/api/overview", "summary stats (nodes/pods/cpu/mem)", "Kubernetes"},
		{"GET", "/api/nodes", "nodes with CPU/MEM/kernel info", "Kubernetes"},
		{"GET", "/api/pods/status", "pod phase counts", "Kubernetes"},
		{"GET", "/api/workloads", "deployments / statefulsets / daemonsets", "Kubernetes"},
		{"GET", "/api/events", "recent cluster events", "Kubernetes"},
		{"GET", "/api/namespaces", "namespaces with pod counts", "Kubernetes"},
		{"GET", "/api/metrics/cluster", "30-min CPU/MEM ring buffer", "Kubernetes"},
		{"GET", "/api/servers", "node-exporter hosts + alerts", "Servers"},
		{"GET", "/api/servers/detail", "single host detail (mem/fs/net)", "Servers"},
		{"GET", "/api/runners", "GitLab CI runners", "GitLab"},
		{"GET", "/api/pipelines", "pipelines grouped by project", "GitLab"},
		{"GET", "/api/pipelines/trends", "24h pipeline success/fail trend", "GitLab"},
		{"POST", "/api/pipelines/action", "retry / cancel a pipeline", "GitLab"},
		{"GET", "/api/tools", "configured external tool URLs", "Meta"},
		{"GET", "/api/about", "this response", "Meta"},
	}

	writeJSON(w, AboutInfo{
		App:       "OBS-Board",
		Uptime:    up.String(),
		Stub:      cfg.Stub,
		Config:    cfgMap,
		Endpoints: endpoints,
	})
}

type ToolItem struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`
	Desc string `json:"desc"`
	URL  string `json:"url"`
}

func handleTools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, []ToolItem{
		{Name: "Grafana", Tag: "metrics", Desc: "Dashboards over Prometheus / metrics-server / node-exporter time-series.", URL: cfg.GrafanaURL},
		{Name: "OpenSearch", Tag: "logs", Desc: "Centralized logs & full-text search across cluster pods and host syslog.", URL: cfg.OpenSearchURL},
		{Name: "Jaeger", Tag: "tracing", Desc: "Distributed tracing for request flows across microservices and ingress.", URL: cfg.JaegerURL},
	})
}
