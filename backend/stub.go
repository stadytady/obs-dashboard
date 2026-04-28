// Stub-режим: отдаёт синтетические данные без подключения к внешним системам
// (k8s, metrics-server, node-exporter, GitLab). Включается флагом -stub.
// Числа слегка "дышат" — чтобы UI выглядел живым.

package main

import (
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"time"
)

type StubServer struct {
	started time.Time
}

func newStubServer() *StubServer {
	return &StubServer{started: time.Now()}
}

func (s *StubServer) register(mux *http.ServeMux) {
	mux.HandleFunc("/api/clusters", s.handleClusters)
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/overview", s.handleOverview)
	mux.HandleFunc("/api/nodes", s.handleNodes)
	mux.HandleFunc("/api/pods/status", s.handlePodStatus)
	mux.HandleFunc("/api/workloads", s.handleWorkloads)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/namespaces", s.handleNamespaces)
	mux.HandleFunc("/api/metrics/cluster", s.handleClusterMetrics)
	mux.HandleFunc("/api/servers", s.handleServers)
	mux.HandleFunc("/api/servers/detail", s.handleServerDetail)
	mux.HandleFunc("/api/runners", s.handleRunners)
	mux.HandleFunc("/api/pipelines", s.handlePipelines)
	mux.HandleFunc("/api/pipelines/action", s.handlePipelineAction)
	mux.HandleFunc("/api/pipelines/trends", s.handlePipelineTrends)
}

func (s *StubServer) handleServerDetail(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	now := time.Now()
	switch name {
	case "edge-2":
		writeJSON(w, ServerDetail{
			Name: "edge-2", URL: "http://10.0.0.12:9100", Up: false, Worst: "down",
			Error: "connection refused", UpdatedAt: now,
			Alerts: []Alert{{Rule: "NodeExporterDown", Severity: "crit", Message: "connection refused"}},
		})
		return
	case "db-1":
		writeJSON(w, ServerDetail{
			Name: "db-1", URL: "http://10.0.0.11:9100", Up: true, Worst: "warn", UpdatedAt: now,
			Uname:         UnameInfo{Hostname: "db-1", Sysname: "Linux", Release: "5.15.0-105-generic", Machine: "x86_64"},
			UptimeSeconds: 7 * 86400, BootTimeUnix: float64(now.Add(-7 * 24 * time.Hour).Unix()),
			CPU: CPUInfo{Cores: 8, Load1: 6.4, Load5: 7.1, Load15: 6.8},
			Mem: MemInfo{TotalBytes: 32e9, AvailableBytes: 3.7e9, FreeBytes: 0.5e9, BuffersBytes: 0.2e9, CachedBytes: 12e9, SwapTotalBytes: 4e9, SwapFreeBytes: 3.8e9},
			Filesystems: []FsInfo{
				{Mountpoint: "/", Fstype: "ext4", SizeBytes: 100e9, AvailBytes: 35e9, UsedPct: 65.0, InodesTotal: 6e6, InodesFree: 5e6, InodesUsedPct: 16.7},
				{Mountpoint: "/var/lib/postgresql", Fstype: "xfs", SizeBytes: 500e9, AvailBytes: 80e9, UsedPct: 84.0, InodesTotal: 200e6, InodesFree: 199e6, InodesUsedPct: 0.5},
			},
			Networks: []NetInfo{
				{Iface: "eth0", RxBytes: 5.2e12, TxBytes: 4.8e12},
			},
			Alerts: []Alert{
				{Rule: "HostHighMemoryUsage", Severity: "warn", Value: 88.3, Message: "memory used 88.3%"},
				{Rule: "HostHighLoadAverage", Severity: "ok", Value: 0.9, Message: "load5/core = 0.91"},
				{Rule: "HostOutOfDiskSpace", Severity: "ok", Value: 0, Message: "all filesystems > 15% free"},
				{Rule: "HostOutOfInodes", Severity: "ok", Value: 0, Message: "all filesystems > 15% inodes free"},
				{Rule: "HostClockSkew", Severity: "ok", Value: 1.5, Message: "ntp offset 1.5 ms"},
			},
		})
		return
	}
	// default: web-1
	writeJSON(w, ServerDetail{
		Name: "web-1", URL: "http://10.0.0.10:9100", Up: true, Worst: "ok", UpdatedAt: now,
		Uname:         UnameInfo{Hostname: "web-1", Sysname: "Linux", Release: "6.1.0-18-amd64", Machine: "x86_64"},
		UptimeSeconds: 42 * 86400, BootTimeUnix: float64(now.Add(-42 * 24 * time.Hour).Unix()),
		CPU: CPUInfo{Cores: 4, Load1: 0.3, Load5: 0.4, Load15: 0.5},
		Mem: MemInfo{TotalBytes: 8e9, AvailableBytes: 4.6e9, FreeBytes: 1.2e9, BuffersBytes: 0.4e9, CachedBytes: 2.8e9, SwapTotalBytes: 2e9, SwapFreeBytes: 2e9},
		Filesystems: []FsInfo{
			{Mountpoint: "/", Fstype: "ext4", SizeBytes: 50e9, AvailBytes: 38e9, UsedPct: 24.0, InodesTotal: 3e6, InodesFree: 2.7e6, InodesUsedPct: 10.0},
			{Mountpoint: "/var", Fstype: "ext4", SizeBytes: 30e9, AvailBytes: 22e9, UsedPct: 26.7, InodesTotal: 2e6, InodesFree: 1.9e6, InodesUsedPct: 5.0},
		},
		Networks: []NetInfo{
			{Iface: "eth0", RxBytes: 1.1e12, TxBytes: 0.9e12},
			{Iface: "wg0", RxBytes: 5.3e9, TxBytes: 4.1e9},
		},
		Alerts: []Alert{
			{Rule: "HostHighMemoryUsage", Severity: "ok", Value: 42.1, Message: "memory used 42.1%"},
			{Rule: "HostHighLoadAverage", Severity: "ok", Value: 0.4, Message: "load5/core = 0.42"},
			{Rule: "HostOutOfDiskSpace", Severity: "ok", Value: 0, Message: "all filesystems > 15% free"},
			{Rule: "HostOutOfInodes", Severity: "ok", Value: 0, Message: "all filesystems > 15% inodes free"},
			{Rule: "HostClockSkew", Severity: "ok", Value: 0.8, Message: "ntp offset 0.8 ms"},
		},
	})
}

func (s *StubServer) handleServers(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	writeJSON(w, []ServerSnapshot{
		{
			Name: "web-1", URL: "http://10.0.0.10:9100",
			Up: true, Worst: "ok", UpdatedAt: now,
			Alerts: []Alert{
				{Rule: "HostHighMemoryUsage", Severity: "ok", Value: 42.1, Message: "memory used 42.1%"},
				{Rule: "HostHighLoadAverage", Severity: "ok", Value: 0.4, Message: "load5/core = 0.42"},
				{Rule: "HostOutOfDiskSpace", Severity: "ok", Value: 0, Message: "all filesystems > 15% free"},
				{Rule: "HostOutOfInodes", Severity: "ok", Value: 0, Message: "all filesystems > 15% inodes free"},
				{Rule: "HostClockSkew", Severity: "ok", Value: 0.8, Message: "ntp offset 0.8 ms"},
			},
		},
		{
			Name: "db-1", URL: "http://10.0.0.11:9100",
			Up: true, Worst: "warn", UpdatedAt: now,
			Alerts: []Alert{
				{Rule: "HostHighMemoryUsage", Severity: "warn", Value: 88.3, Message: "memory used 88.3%"},
				{Rule: "HostHighLoadAverage", Severity: "ok", Value: 0.9, Message: "load5/core = 0.91"},
				{Rule: "HostOutOfDiskSpace", Severity: "ok", Value: 0, Message: "all filesystems > 15% free"},
				{Rule: "HostOutOfInodes", Severity: "ok", Value: 0, Message: "all filesystems > 15% inodes free"},
				{Rule: "HostClockSkew", Severity: "ok", Value: 1.5, Message: "ntp offset 1.5 ms"},
			},
		},
		{
			Name: "edge-2", URL: "http://10.0.0.12:9100",
			Up: false, Worst: "down", UpdatedAt: now, Error: "connection refused",
			Alerts: []Alert{
				{Rule: "NodeExporterDown", Severity: "crit", Message: "connection refused"},
			},
		},
	})
}

func (s *StubServer) handleClusters(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, []ClusterListItem{{Name: "stub", K8s: true, Metrics: true}})
}

// base + sin-волна + небольшой шум, зажато в [0,100].
func (s *StubServer) wobble(base, amp, periodSec float64) float64 {
	t := time.Since(s.started).Seconds()
	v := base + math.Sin(t/periodSec)*amp + (rand.Float64()-0.5)*amp/3
	return clamp01(v)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (s *StubServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]interface{}{"cluster": "stub", "k8s": true, "metrics": true})
}

func (s *StubServer) handleOverview(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, OverviewResp{
		Nodes:       3,
		Pods:        47,
		Deployments: 12,
		Services:    18,
		CPUUsed:     round1(s.wobble(34, 10, 17)),
		MemUsed:     round1(s.wobble(58, 7, 23)),
	})
}

func (s *StubServer) handleNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, []NodeResp{
		{
			Name: "k3s-master-1", Role: "master,etcd", Status: "ready",
			CPU: round1(s.wobble(18, 6, 13)), Mem: round1(s.wobble(42, 5, 19)),
			Pods: 14, Age: "42d",
			Kernel: "5.15.0-105-generic", OS: "Ubuntu 22.04.4 LTS", Kubelet: "v1.30.1+k3s1",
		},
		{
			Name: "k3s-worker-1", Role: "worker", Status: "ready",
			CPU: round1(s.wobble(55, 14, 11)), Mem: round1(s.wobble(71, 8, 17)),
			Pods: 18, Age: "38d",
			Kernel: "5.15.0-105-generic", OS: "Ubuntu 22.04.4 LTS", Kubelet: "v1.30.1+k3s1",
		},
		{
			Name: "k3s-worker-2", Role: "worker", Status: "warn",
			CPU: round1(s.wobble(88, 5, 9)), Mem: round1(s.wobble(91, 3, 15)),
			Pods: 15, Age: "22d",
			Kernel: "6.1.0-18-amd64", OS: "Debian GNU/Linux 12 (bookworm)", Kubelet: "v1.30.1+k3s1",
		},
	})
}

func (s *StubServer) handlePodStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, PodStatusResp{Running: 42, Pending: 2, Succeeded: 1, Failed: 1, Unknown: 1})
}

func (s *StubServer) handleWorkloads(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, []WorkloadResp{
		{Name: "traefik", NS: "kube-system", Kind: "deploy", Replicas: 1, Ready: 0, Age: "2h"},
		{Name: "ingress-nginx-controller", NS: "ingress-nginx", Kind: "ds", Replicas: 3, Ready: 2, Age: "30d"},
		{Name: "coredns", NS: "kube-system", Kind: "deploy", Replicas: 2, Ready: 2, Age: "42d"},
		{Name: "grafana", NS: "monitoring", Kind: "deploy", Replicas: 1, Ready: 1, Age: "30d"},
		{Name: "metrics-server", NS: "kube-system", Kind: "deploy", Replicas: 1, Ready: 1, Age: "42d"},
		{Name: "my-app-backend", NS: "default", Kind: "deploy", Replicas: 3, Ready: 3, Age: "5d"},
		{Name: "my-app-frontend", NS: "default", Kind: "deploy", Replicas: 2, Ready: 2, Age: "5d"},
		{Name: "node-exporter", NS: "monitoring", Kind: "ds", Replicas: 3, Ready: 3, Age: "30d"},
		{Name: "prometheus-server", NS: "monitoring", Kind: "sts", Replicas: 1, Ready: 1, Age: "30d"},
		{Name: "redis", NS: "default", Kind: "sts", Replicas: 1, Ready: 1, Age: "5d"},
	})
}

func (s *StubServer) handleEvents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, []EventResp{
		{Time: "now", Type: "warning", Msg: "<strong>kube-system/traefik-6f9c4b5d9c-x2k7p</strong> BackOff — Back-off restarting failed container"},
		{Time: "2m", Type: "normal", Msg: "<strong>default/my-app-backend-5d9c4b5-abc12</strong> Started — Started container backend"},
		{Time: "5m", Type: "error", Msg: "<strong>monitoring/grafana-7c9f4c5b8-qwe23</strong> Unhealthy — Readiness probe failed: connection refused"},
		{Time: "12m", Type: "normal", Msg: "<strong>default/my-app-frontend-5b8c9d-xyz98</strong> Pulled — Successfully pulled image in 3.4s"},
		{Time: "18m", Type: "normal", Msg: "<strong>kube-system/coredns-5d6b7c8d9-klm45</strong> Killing — Stopping container coredns"},
		{Time: "1h", Type: "warning", Msg: "<strong>default/redis-0</strong> FailedMount — Unable to attach or mount volumes"},
		{Time: "3h", Type: "normal", Msg: "<strong>monitoring/prometheus-server-0</strong> Scheduled — Successfully assigned to k3s-worker-1"},
	})
}

func (s *StubServer) handleNamespaces(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, []NamespaceResp{
		{Name: "default", Pods: 8, Bad: 0},
		{Name: "ingress-nginx", Pods: 3, Bad: 1},
		{Name: "kube-public", Pods: 0, Bad: 0},
		{Name: "kube-system", Pods: 12, Bad: 1},
		{Name: "monitoring", Pods: 8, Bad: 1},
	})
}

func (s *StubServer) handleClusterMetrics(w http.ResponseWriter, _ *http.Request) {
	end := time.Now()
	cpu := make([][2]float64, 0, 30)
	mem := make([][2]float64, 0, 30)
	for i := 30; i > 0; i-- {
		ts := float64(end.Add(-time.Duration(i) * time.Minute).Unix())
		x := float64(i)
		cpu = append(cpu, [2]float64{ts, clamp01(35 + math.Sin(x/4)*15 + rand.Float64()*4)})
		mem = append(mem, [2]float64{ts, clamp01(60 + math.Sin(x/7)*6 + rand.Float64()*3)})
	}
	writeJSON(w, ClusterMetricsResp{
		CPU: Series{Points: cpu},
		Mem: Series{Points: mem},
	})
}

func (s *StubServer) handlePipelines(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	writeJSON(w, []PipelineProject{
		{
			ID: 101, Name: "backend-api", FullPath: "group/backend-api",
			WebURL: "https://gitlab.example.com/group/backend-api",
			Pipelines: []PipelineItem{
				{ID: 1001, Status: "running", Ref: "main", SHA: "a1b2c3d", CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339), UpdatedAt: now.Add(-30 * time.Second).Format(time.RFC3339), Duration: 0, User: "alice", WebURL: "https://gitlab.example.com/group/backend-api/-/pipelines/1001"},
				{ID: 1000, Status: "success", Ref: "main", SHA: "b2c3d4e", CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339), UpdatedAt: now.Add(-90 * time.Minute).Format(time.RFC3339), Duration: 184, User: "alice", WebURL: "https://gitlab.example.com/group/backend-api/-/pipelines/1000"},
			},
		},
		{
			ID: 102, Name: "frontend-app", FullPath: "group/frontend-app",
			WebURL: "https://gitlab.example.com/group/frontend-app",
			Pipelines: []PipelineItem{
				{ID: 2001, Status: "failed", Ref: "feature/auth", SHA: "c3d4e5f", CreatedAt: now.Add(-15 * time.Minute).Format(time.RFC3339), UpdatedAt: now.Add(-12 * time.Minute).Format(time.RFC3339), Duration: 97, User: "bob", WebURL: "https://gitlab.example.com/group/frontend-app/-/pipelines/2001"},
				{ID: 2000, Status: "success", Ref: "main", SHA: "d4e5f6a", CreatedAt: now.Add(-4 * time.Hour).Format(time.RFC3339), UpdatedAt: now.Add(-3*time.Hour - 45*time.Minute).Format(time.RFC3339), Duration: 312, User: "alice", WebURL: "https://gitlab.example.com/group/frontend-app/-/pipelines/2000"},
			},
		},
		{
			ID: 103, Name: "infra-terraform", FullPath: "ops/infra-terraform",
			WebURL: "https://gitlab.example.com/ops/infra-terraform",
			Pipelines: []PipelineItem{
				{ID: 3001, Status: "canceled", Ref: "feat/k8s-upgrade", SHA: "e5f6a7b", CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339), UpdatedAt: now.Add(-28 * time.Minute).Format(time.RFC3339), Duration: 45, User: "charlie", WebURL: "https://gitlab.example.com/ops/infra-terraform/-/pipelines/3001"},
				{ID: 3000, Status: "success", Ref: "main", SHA: "f6a7b8c", CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339), UpdatedAt: now.Add(-23*time.Hour - 30*time.Minute).Format(time.RFC3339), Duration: 420, User: "charlie", WebURL: "https://gitlab.example.com/ops/infra-terraform/-/pipelines/3000"},
			},
		},
	})
}

func (s *StubServer) handlePipelineAction(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *StubServer) handlePipelineTrends(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	statuses := []string{"success", "success", "success", "failed", "success", "success", "canceled", "success", "failed", "success", "success", "success", "running"}
	refs := []string{"main", "main", "feat/auth", "main", "main", "fix/typo", "main", "main", "main", "feat/auth", "main", "main", "main"}
	out := make([]PipelineTrendItem, len(statuses))
	for i, st := range statuses {
		dur := 90 + i*15
		if st == "running" {
			dur = 0
		}
		out[i] = PipelineTrendItem{
			ID:        5000 + i,
			Status:    st,
			Ref:       refs[i],
			CreatedAt: now.Add(-time.Duration(len(statuses)-i) * 90 * time.Minute).Format(time.RFC3339),
			Duration:  dur,
			WebURL:    "https://gitlab.example.com/-/pipelines/" + fmt.Sprintf("%d", 5000+i),
		}
	}
	writeJSON(w, out)
}

func (s *StubServer) handleRunners(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	writeJSON(w, []RunnerItem{
		{ID: 1, Description: "docker-runner-01", Status: "online", Paused: false, IsShared: true, RunnerType: "instance_type", Tags: []string{"docker", "linux"}, ContactedAt: now.Add(-18 * time.Second).Format(time.RFC3339)},
		{ID: 2, Description: "k8s-executor", Status: "online", Paused: false, IsShared: true, RunnerType: "instance_type", Tags: []string{"kubernetes", "docker"}, ContactedAt: now.Add(-2 * time.Minute).Format(time.RFC3339)},
		{ID: 3, Description: "staging-group-runner", Status: "online", Paused: true, IsShared: false, RunnerType: "group_type", Tags: []string{"staging", "docker"}, ContactedAt: now.Add(-7 * time.Minute).Format(time.RFC3339)},
		{ID: 4, Description: "shell-runner-legacy", Status: "offline", Paused: false, IsShared: false, RunnerType: "project_type", Tags: []string{"shell"}, ContactedAt: now.Add(-72 * time.Hour).Format(time.RFC3339)},
		{ID: 5, Description: "windows-runner", Status: "stale", Paused: false, IsShared: false, RunnerType: "group_type", Tags: []string{"windows", "powershell"}, ContactedAt: now.Add(-30 * time.Minute).Format(time.RFC3339)},
		{ID: 6, Description: "arm64-builder", Status: "online", Paused: false, IsShared: true, RunnerType: "instance_type", Tags: []string{"arm64", "docker"}, ContactedAt: now.Add(-45 * time.Second).Format(time.RFC3339)},
	})
}
