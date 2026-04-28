// kube-ctl backend — read-only API для дашборда.
// Данные:
//   - k8s:        client-go (kubeconfig или in-cluster ServiceAccount)
//   - CPU/MEM:    metrics.k8s.io/v1beta1 (metrics-server)
//   - host-stats: scrape node-exporter /metrics (servers.go)
//   - GitLab CI:  /api/v4 + /api/graphql
//
// Структура пакета:
//   main.go      — Config, флаги, main(), healthcheck
//   routes.go    — registerLiveRoutes: wiring всех хендлеров
//   about.go     — /api/about, /api/tools
//   clusters.go  — Cluster, MultiCluster registry, /api/cluster*-хендлеры
//   servers.go   — node-exporter scraping + alert rules + /api/servers*
//   gitlab.go    — GitLab Runners (GraphQL+REST), runners cache, glab config
//   helpers.go   — middleware, JSON-ответы, форматирование
//   stub.go      — синтетические данные при -stub

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/util/homedir"
)

type Config struct {
	Addr          string
	Kubeconfig    string // single-cluster shortcut
	ClustersFile  string // multi-cluster YAML
	ServersFile   string // node-exporter hosts YAML
	StaticDir     string
	Stub          bool
	GitLabURL     string // GitLab instance URL
	GitLabToken   string // GitLab private API token
	GrafanaURL    string
	OpenSearchURL string
	JaegerURL     string
}

var cfg Config

func main() {
	home := homedir.HomeDir()
	defaultKubeconfig := ""
	if home != "" {
		defaultKubeconfig = filepath.Join(home, ".kube", "config")
	}

	flag.StringVar(&cfg.Addr, "addr", ":8080", "listen address")
	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", defaultKubeconfig, "path to kubeconfig (single-cluster shortcut; ignored if -clusters set)")
	flag.StringVar(&cfg.ClustersFile, "clusters", "", "path to clusters YAML (multi-cluster mode)")
	flag.StringVar(&cfg.ServersFile, "servers", "", "path to servers YAML (node-exporter hosts)")
	flag.StringVar(&cfg.StaticDir, "static", "./static", "static files directory")
	flag.BoolVar(&cfg.Stub, "stub", false, "mock all data (no external connections)")
	flag.StringVar(&cfg.GitLabURL, "gitlab-url", os.Getenv("GITLAB_URL"), "GitLab instance URL (e.g. https://gitlab.example.com, or set GITLAB_URL env var)")
	flag.StringVar(&cfg.GitLabToken, "gitlab-token", os.Getenv("GITLAB_TOKEN"), "GitLab private API token (or set GITLAB_TOKEN env var)")
	flag.StringVar(&cfg.GrafanaURL, "grafana-url", os.Getenv("GRAFANA_URL"), "Grafana URL (e.g. https://grafana.example.com)")
	flag.StringVar(&cfg.OpenSearchURL, "opensearch-url", os.Getenv("OPENSEARCH_URL"), "OpenSearch URL")
	flag.StringVar(&cfg.JaegerURL, "jaeger-url", os.Getenv("JAEGER_URL"), "Jaeger URL")
	healthcheck := flag.Bool("healthcheck", false, "probe -addr/healthz from inside the container (exit 0 OK, 1 fail) — для Docker HEALTHCHECK")
	flag.Parse()

	if *healthcheck {
		runHealthcheck(cfg.Addr)
		return // unreachable: runHealthcheck вызывает os.Exit
	}

	// Автоопределение из конфига glab, если флаги не заданы явно.
	if cfg.GitLabURL == "" || cfg.GitLabToken == "" {
		if u, t := readGLabConfig(); u != "" && t != "" {
			if cfg.GitLabURL == "" {
				cfg.GitLabURL = u
			}
			if cfg.GitLabToken == "" {
				cfg.GitLabToken = t
			}
			log.Printf("gitlab: loaded credentials from glab config (%s)", cfg.GitLabURL)
		}
	}

	mux := http.NewServeMux()

	if cfg.Stub {
		log.Printf("STUB MODE — все данные синтетические, к кластеру не подключаемся")
		newStubServer().register(mux)
	} else {
		registerLiveRoutes(mux)
	}

	mux.HandleFunc("/api/tools", handleTools)
	mux.HandleFunc("/api/about", handleAbout)
	mux.Handle("/", http.FileServer(http.Dir(cfg.StaticDir)))

	handler := withCORS(withLogging(mux))

	log.Printf("kube-ctl listening on %s (static=%s, stub=%v)", cfg.Addr, cfg.StaticDir, cfg.Stub)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}

// runHealthcheck — самопроверка для Docker HEALTHCHECK / k8s readiness probe.
// Дёргает /api/clusters на собственном listen-адресе, exit 0 при HTTP 2xx, иначе 1.
func runHealthcheck(addr string) {
	target := addr
	if strings.HasPrefix(target, ":") {
		target = "127.0.0.1" + target
	}
	url := "http://" + target + "/api/clusters"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}
