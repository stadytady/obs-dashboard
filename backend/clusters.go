// k8s-кластеры: конфиг, MultiCluster registry, ring buffer метрик, все
// /api/cluster*-хендлеры (overview/nodes/pods/workloads/events/namespaces).

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
	"sigs.k8s.io/yaml"
)

// ========== config ==========

type ClusterSpec struct {
	Name       string `json:"name"       yaml:"name"`
	Kubeconfig string `json:"kubeconfig" yaml:"kubeconfig"`
}

type clustersFileYAML struct {
	Clusters []ClusterSpec `json:"clusters" yaml:"clusters"`
}

func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		return rest.InClusterConfig()
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

// resolveClusterSpecs превращает флаги в список кластеров.
// Приоритет: -clusters YAML > одиночный -kubeconfig (имя "default").
func resolveClusterSpecs(c Config) ([]ClusterSpec, error) {
	if c.ClustersFile != "" {
		raw, err := os.ReadFile(c.ClustersFile)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", c.ClustersFile, err)
		}
		var f clustersFileYAML
		if err := yaml.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("parse %s: %w", c.ClustersFile, err)
		}
		if len(f.Clusters) == 0 {
			return nil, fmt.Errorf("%s: no clusters defined", c.ClustersFile)
		}
		seen := map[string]bool{}
		for i, sp := range f.Clusters {
			if sp.Name == "" {
				return nil, fmt.Errorf("cluster #%d: name is empty", i)
			}
			if seen[sp.Name] {
				return nil, fmt.Errorf("duplicate cluster name %q", sp.Name)
			}
			seen[sp.Name] = true
		}
		return f.Clusters, nil
	}
	return []ClusterSpec{{
		Name:       "default",
		Kubeconfig: c.Kubeconfig,
	}}, nil
}

// ========== Cluster + MultiCluster registry ==========

type Cluster struct {
	Name    string
	K8s     *kubernetes.Clientset
	Metrics *metricsv.Clientset
	Series  *SeriesBuffer // 30-минутный ring buffer средних CPU/MEM по кластеру
}

// MultiCluster держит набор активных Cluster'ов и роутит API-запросы по
// query-параметру ?cluster=<name>. Назван явно (а не Server), чтобы не
// сталкиваться с http.Server и не путаться с ServersHub из servers.go.
type MultiCluster struct {
	clusters map[string]*Cluster
	order    []string
}

// pick выбирает кластер по ?cluster=<name>; без параметра — первый в порядке объявления.
func (mc *MultiCluster) pick(r *http.Request) (*Cluster, error) {
	name := r.URL.Query().Get("cluster")
	if name == "" {
		if len(mc.order) == 0 {
			return nil, fmt.Errorf("no clusters configured")
		}
		name = mc.order[0]
	}
	c, ok := mc.clusters[name]
	if !ok {
		return nil, fmt.Errorf("unknown cluster %q", name)
	}
	return c, nil
}

// ========== ring buffer для /api/metrics/cluster ==========

type SeriesPoint struct {
	TS  int64
	CPU float64
	Mem float64
}

type SeriesBuffer struct {
	mu     sync.RWMutex
	cap    int
	points []SeriesPoint
}

func (b *SeriesBuffer) Push(p SeriesPoint) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.points = append(b.points, p)
	if len(b.points) > b.cap {
		b.points = b.points[len(b.points)-b.cap:]
	}
}

func (b *SeriesBuffer) Snapshot() []SeriesPoint {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]SeriesPoint, len(b.points))
	copy(out, b.points)
	return out
}

// pollClusterSeries раз в 60s сэмплирует среднюю загрузку по кластеру через
// metrics-server и пишет в ring buffer. Цикл живёт до завершения процесса.
func pollClusterSeries(cl *Cluster) {
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	sample := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cpu, mem, err := computeClusterUsage(ctx, cl)
		if err != nil {
			log.Printf("metrics poll %q: %v", cl.Name, err)
			return
		}
		cl.Series.Push(SeriesPoint{TS: time.Now().Unix(), CPU: cpu, Mem: mem})
	}
	sample()
	for range tick.C {
		sample()
	}
}

// computeClusterUsage: avg по нодам, проценты от Capacity.
func computeClusterUsage(ctx context.Context, cl *Cluster) (cpuPct, memPct float64, err error) {
	nm, err := cl.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, 0, err
	}
	nodes, err := cl.K8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, 0, err
	}
	type capPair struct{ cpu, mem float64 }
	caps := make(map[string]capPair, len(nodes.Items))
	for _, n := range nodes.Items {
		caps[n.Name] = capPair{
			cpu: n.Status.Capacity.Cpu().AsApproximateFloat64(),
			mem: n.Status.Capacity.Memory().AsApproximateFloat64(),
		}
	}
	var usedCPU, capCPU, usedMem, capMem float64
	for _, m := range nm.Items {
		c, ok := caps[m.Name]
		if !ok {
			continue
		}
		usedCPU += m.Usage.Cpu().AsApproximateFloat64()
		usedMem += m.Usage.Memory().AsApproximateFloat64()
		capCPU += c.cpu
		capMem += c.mem
	}
	if capCPU > 0 {
		cpuPct = usedCPU / capCPU * 100
	}
	if capMem > 0 {
		memPct = usedMem / capMem * 100
	}
	return cpuPct, memPct, nil
}

// ========== /api/clusters ==========

type ClusterListItem struct {
	Name    string `json:"name"`
	K8s     bool   `json:"k8s"`
	Metrics bool   `json:"metrics"` // metrics-server доступен
}

// handleClusters параллельно проверяет health всех кластеров.
func (mc *MultiCluster) handleClusters(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	out := make([]ClusterListItem, len(mc.order))
	var wg sync.WaitGroup
	for i, name := range mc.order {
		out[i] = ClusterListItem{Name: name}
		wg.Add(1)
		go func(idx int, cl *Cluster) {
			defer wg.Done()
			if _, err := cl.K8s.Discovery().ServerVersion(); err == nil {
				out[idx].K8s = true
			}
			if _, err := cl.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{Limit: 1}); err == nil {
				out[idx].Metrics = true
			}
		}(i, mc.clusters[name])
	}
	wg.Wait()
	writeJSON(w, out)
}

// ========== /api/health ==========

func (mc *MultiCluster) handleHealth(w http.ResponseWriter, r *http.Request) {
	c, err := mc.pick(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if _, err := c.K8s.Discovery().ServerVersion(); err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	metricsOK := false
	if _, err := c.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{Limit: 1}); err == nil {
		metricsOK = true
	}
	writeJSON(w, map[string]interface{}{"cluster": c.Name, "k8s": true, "metrics": metricsOK})
}

// ========== /api/overview ==========

type OverviewResp struct {
	Nodes       int     `json:"nodes"`
	Pods        int     `json:"pods"`
	Deployments int     `json:"deployments"`
	Services    int     `json:"services"`
	CPUUsed     float64 `json:"cpuUsed"` // %
	MemUsed     float64 `json:"memUsed"` // %
}

func (mc *MultiCluster) handleOverview(w http.ResponseWriter, r *http.Request) {
	c, err := mc.pick(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	nodes, err := c.K8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	pods, err := c.K8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	deps, err := c.K8s.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	svcs, err := c.K8s.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}

	// Средние значения по кластеру через metrics-server (sum used / sum capacity).
	cpu, mem, _ := computeClusterUsage(ctx, c)

	writeJSON(w, OverviewResp{
		Nodes:       len(nodes.Items),
		Pods:        len(pods.Items),
		Deployments: len(deps.Items),
		Services:    len(svcs.Items),
		CPUUsed:     round1(cpu),
		MemUsed:     round1(mem),
	})
}

// ========== /api/nodes ==========

type NodeResp struct {
	Name    string  `json:"name"`
	Role    string  `json:"role"`
	Status  string  `json:"status"` // ready | warn | err
	CPU     float64 `json:"cpu"`    // %
	Mem     float64 `json:"mem"`    // %
	Pods    int     `json:"pods"`
	Age     string  `json:"age"`
	Kernel  string  `json:"kernel"`  // 5.15.0-105-generic
	OS      string  `json:"os"`      // Ubuntu 22.04.4 LTS
	Kubelet string  `json:"kubelet"` // v1.30.1+k3s1
}

func (mc *MultiCluster) handleNodes(w http.ResponseWriter, r *http.Request) {
	c, err := mc.pick(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	nodes, err := c.K8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	pods, err := c.K8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}

	podsPerNode := map[string]int{}
	for _, p := range pods.Items {
		if p.Spec.NodeName != "" {
			podsPerNode[p.Spec.NodeName]++
		}
	}

	// CPU/MEM по нодам — из metrics-server (best-effort).
	type usage struct{ cpu, mem float64 }
	useByNode := map[string]usage{}
	if nm, err := c.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{}); err == nil {
		for _, m := range nm.Items {
			useByNode[m.Name] = usage{
				cpu: m.Usage.Cpu().AsApproximateFloat64(),
				mem: m.Usage.Memory().AsApproximateFloat64(),
			}
		}
	}

	out := make([]NodeResp, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		role := nodeRole(&n)
		status := nodeStatus(&n)

		var cpu, mem float64
		if u, ok := useByNode[n.Name]; ok {
			if cap := n.Status.Capacity.Cpu().AsApproximateFloat64(); cap > 0 {
				cpu = u.cpu / cap * 100
			}
			if cap := n.Status.Capacity.Memory().AsApproximateFloat64(); cap > 0 {
				mem = u.mem / cap * 100
			}
		}

		// эвристика warn по загрузке
		if status == "ready" && (cpu >= 90 || mem >= 90) {
			status = "warn"
		}

		out = append(out, NodeResp{
			Name:    n.Name,
			Role:    role,
			Status:  status,
			CPU:     round1(cpu),
			Mem:     round1(mem),
			Pods:    podsPerNode[n.Name],
			Age:     humanAge(n.CreationTimestamp.Time),
			Kernel:  n.Status.NodeInfo.KernelVersion,
			OS:      n.Status.NodeInfo.OSImage,
			Kubelet: n.Status.NodeInfo.KubeletVersion,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, out)
}

func nodeRole(n *corev1.Node) string {
	var roles []string
	for k := range n.Labels {
		if strings.HasPrefix(k, "node-role.kubernetes.io/") {
			r := strings.TrimPrefix(k, "node-role.kubernetes.io/")
			if r != "" {
				roles = append(roles, r)
			}
		}
	}
	sort.Strings(roles)
	if len(roles) == 0 {
		return "worker"
	}
	return strings.Join(roles, ",")
}

func nodeStatus(n *corev1.Node) string {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			if c.Status == corev1.ConditionTrue {
				return "ready"
			}
			return "err"
		}
	}
	return "err"
}

// ========== /api/pods/status ==========

type PodStatusResp struct {
	Running   int `json:"running"`
	Pending   int `json:"pending"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Unknown   int `json:"unknown"`
}

func (mc *MultiCluster) handlePodStatus(w http.ResponseWriter, r *http.Request) {
	c, err := mc.pick(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	pods, err := c.K8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	var res PodStatusResp
	for _, p := range pods.Items {
		switch p.Status.Phase {
		case corev1.PodRunning:
			res.Running++
		case corev1.PodPending:
			res.Pending++
		case corev1.PodSucceeded:
			res.Succeeded++
		case corev1.PodFailed:
			res.Failed++
		default:
			res.Unknown++
		}
	}
	writeJSON(w, res)
}

// ========== /api/workloads ==========

type WorkloadResp struct {
	Name     string `json:"name"`
	NS       string `json:"ns"`
	Kind     string `json:"kind"` // deploy | sts | ds
	Replicas int    `json:"replicas"`
	Ready    int    `json:"ready"`
	Age      string `json:"age"`
}

func (mc *MultiCluster) handleWorkloads(w http.ResponseWriter, r *http.Request) {
	c, err := mc.pick(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	var out []WorkloadResp

	deps, err := c.K8s.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	for _, d := range deps.Items {
		out = append(out, WorkloadResp{
			Name: d.Name, NS: d.Namespace, Kind: "deploy",
			Replicas: int(ptrInt32(d.Spec.Replicas)),
			Ready:    int(d.Status.ReadyReplicas),
			Age:      humanAge(d.CreationTimestamp.Time),
		})
	}

	sts, err := c.K8s.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	for _, st := range sts.Items {
		out = append(out, WorkloadResp{
			Name: st.Name, NS: st.Namespace, Kind: "sts",
			Replicas: int(ptrInt32(st.Spec.Replicas)),
			Ready:    int(st.Status.ReadyReplicas),
			Age:      humanAge(st.CreationTimestamp.Time),
		})
	}

	ds, err := c.K8s.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	for _, d := range ds.Items {
		out = append(out, WorkloadResp{
			Name: d.Name, NS: d.Namespace, Kind: "ds",
			Replicas: int(d.Status.DesiredNumberScheduled),
			Ready:    int(d.Status.NumberReady),
			Age:      humanAge(d.CreationTimestamp.Time),
		})
	}

	// сначала "проблемные" (ready < replicas), потом по имени
	sort.SliceStable(out, func(i, j int) bool {
		pi := out[i].Ready < out[i].Replicas
		pj := out[j].Ready < out[j].Replicas
		if pi != pj {
			return pi
		}
		return out[i].NS+"/"+out[i].Name < out[j].NS+"/"+out[j].Name
	})
	writeJSON(w, out)
}

// ========== /api/events ==========

type EventResp struct {
	Time string `json:"t"`
	Type string `json:"type"` // normal | warning | error
	Msg  string `json:"msg"`
}

func (mc *MultiCluster) handleEvents(w http.ResponseWriter, r *http.Request) {
	c, err := mc.pick(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	evs, err := c.K8s.CoreV1().Events("").List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	items := append([]corev1.Event(nil), evs.Items...)
	sort.Slice(items, func(i, j int) bool {
		return eventTime(items[i]).After(eventTime(items[j]))
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]EventResp, 0, len(items))
	for _, e := range items {
		t := strings.ToLower(e.Type)
		if t == "" {
			t = "normal"
		}
		// error эвристика: Warning + типичные reason'ы
		if t == "warning" && (strings.Contains(e.Reason, "Failed") ||
			strings.Contains(e.Reason, "BackOff") ||
			strings.Contains(e.Reason, "Unhealthy")) {
			t = "error"
		}
		msg := fmt.Sprintf("<strong>%s/%s</strong> %s — %s",
			e.InvolvedObject.Namespace, e.InvolvedObject.Name, e.Reason, e.Message)
		out = append(out, EventResp{
			Time: humanAgeShort(eventTime(e)),
			Type: t,
			Msg:  msg,
		})
	}
	writeJSON(w, out)
}

// eventTime: события из events.k8s.io/v1 кладут время в EventTime,
// у legacy core/v1 — в LastTimestamp/FirstTimestamp. Возвращаем первое непустое.
func eventTime(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	if !e.FirstTimestamp.IsZero() {
		return e.FirstTimestamp.Time
	}
	return e.CreationTimestamp.Time
}

// ========== /api/namespaces ==========

type NamespaceResp struct {
	Name string `json:"name"`
	Pods int    `json:"pods"`
	Bad  int    `json:"bad"`
}

func (mc *MultiCluster) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	c, err := mc.pick(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	nss, err := c.K8s.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	pods, err := c.K8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeErr(w, 500, err)
		return
	}

	stats := map[string]*NamespaceResp{}
	for _, n := range nss.Items {
		stats[n.Name] = &NamespaceResp{Name: n.Name}
	}
	for _, p := range pods.Items {
		ns, ok := stats[p.Namespace]
		if !ok {
			ns = &NamespaceResp{Name: p.Namespace}
			stats[p.Namespace] = ns
		}
		ns.Pods++
		if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodSucceeded {
			ns.Bad++
		}
	}
	out := make([]NamespaceResp, 0, len(stats))
	for _, v := range stats {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, out)
}

// ========== /api/metrics/cluster ==========

type Series struct {
	Points [][2]float64 `json:"points"` // [timestamp, value]
}

type ClusterMetricsResp struct {
	CPU Series `json:"cpu"`
	Mem Series `json:"mem"`
}

func (mc *MultiCluster) handleClusterMetrics(w http.ResponseWriter, r *http.Request) {
	c, err := mc.pick(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pts := c.Series.Snapshot()
	cpuPts := make([][2]float64, 0, len(pts))
	memPts := make([][2]float64, 0, len(pts))
	for _, p := range pts {
		cpuPts = append(cpuPts, [2]float64{float64(p.TS), p.CPU})
		memPts = append(memPts, [2]float64{float64(p.TS), p.Mem})
	}
	writeJSON(w, ClusterMetricsResp{
		CPU: Series{Points: cpuPts},
		Mem: Series{Points: memPts},
	})
}
