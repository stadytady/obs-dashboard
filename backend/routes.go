package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// registerLiveRoutes — кластерные хендлеры + опциональные servers/runners.
func registerLiveRoutes(mux *http.ServeMux) {
	specs, err := resolveClusterSpecs(cfg)
	if err != nil {
		log.Fatalf("clusters config: %v", err)
	}
	mc := &MultiCluster{clusters: map[string]*Cluster{}}
	for _, sp := range specs {
		restCfg, err := buildRestConfig(sp.Kubeconfig)
		if err != nil {
			log.Fatalf("cluster %q rest config: %v", sp.Name, err)
		}
		k8s, err := kubernetes.NewForConfig(restCfg)
		if err != nil {
			log.Fatalf("cluster %q k8s client: %v", sp.Name, err)
		}
		mcli, err := metricsv.NewForConfig(restCfg)
		if err != nil {
			log.Fatalf("cluster %q metrics client: %v", sp.Name, err)
		}
		cl := &Cluster{
			Name:    sp.Name,
			K8s:     k8s,
			Metrics: mcli,
			Series:  &SeriesBuffer{cap: 30},
		}
		mc.clusters[sp.Name] = cl
		mc.order = append(mc.order, sp.Name)
		go pollClusterSeries(cl)
		log.Printf("cluster %q: kubeconfig=%s (metrics-server)", sp.Name, sp.Kubeconfig)
	}

	mux.HandleFunc("/api/clusters", mc.handleClusters)
	mux.HandleFunc("/api/health", mc.handleHealth)
	mux.HandleFunc("/api/overview", mc.handleOverview)
	mux.HandleFunc("/api/nodes", mc.handleNodes)
	mux.HandleFunc("/api/pods/status", mc.handlePodStatus)
	mux.HandleFunc("/api/workloads", mc.handleWorkloads)
	mux.HandleFunc("/api/events", mc.handleEvents)
	mux.HandleFunc("/api/namespaces", mc.handleNamespaces)
	mux.HandleFunc("/api/metrics/cluster", mc.handleClusterMetrics)

	if cfg.ServersFile != "" {
		specs, err := loadServersFile(cfg.ServersFile)
		if err != nil {
			log.Fatalf("servers config: %v", err)
		}
		hub := NewServersHub(specs)
		hub.Start(context.Background(), 30*time.Second)
		mux.HandleFunc("/api/servers", hub.handleList)
		mux.HandleFunc("/api/servers/detail", hub.handleDetail)
		for _, sp := range specs {
			log.Printf("server %q: %s", sp.Name, sp.URL)
		}
	} else {
		// пустой ответ — фронт по нему понимает, что фичи нет, и прячет дропдаун.
		mux.HandleFunc("/api/servers", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, []any{})
		})
	}

	if cfg.GitLabURL != "" && cfg.GitLabToken != "" {
		gl := newGitLabClient(cfg.GitLabURL, cfg.GitLabToken)
		mux.HandleFunc("/api/runners", handleRunners(gl))
		mux.HandleFunc("/api/pipelines", handlePipelines(gl))
		mux.HandleFunc("/api/pipelines/action", handlePipelineAction(gl))
		mux.HandleFunc("/api/pipelines/trends", handlePipelineTrends(gl))
		log.Printf("gitlab runners+pipelines: %s", cfg.GitLabURL)
	} else {
		mux.HandleFunc("/api/runners", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, []RunnerItem{})
		})
		mux.HandleFunc("/api/pipelines", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, []PipelineProject{})
		})
	}
}
