// Servers: poll node-exporter (:9100/metrics) на списке хостов из YAML
// и прогон 5 базовых правил в духе awesome-prometheus-alerts (без rate,
// одиночный сэмпл — без хранения предыдущего значения).
//
// Endpoints:
//   GET /api/servers — список + worst severity + кол-во активных алертов
//
// Конфиг:
//   servers:
//     - name: web-1
//       url: http://192.168.10.10:9100

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"sigs.k8s.io/yaml"
)

// prometheus/common >=0.62 требует явную схему валидации имён метрик —
// без неё TextParser паникует в IsValidMetricName: "Invalid name validation scheme: unset".
// У TextParser своё внутреннее поле scheme (не читает глобальную NameValidationScheme),
// так что инстанс создаётся через NewTextParser(model.LegacyValidation).
//
// Глобальную тоже задаём — на случай если другие пакеты её читают.
func init() {
	model.NameValidationScheme = model.LegacyValidation
}

// ========== config ==========

type ServerSpec struct {
	Name string `json:"name" yaml:"name"`
	URL  string `json:"url"  yaml:"url"` // http://host:9100
}

type serversFileYAML struct {
	Servers []ServerSpec `json:"servers" yaml:"servers"`
}

func loadServersFile(path string) ([]ServerSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f serversFileYAML
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(f.Servers) == 0 {
		return nil, fmt.Errorf("%s: no servers defined", path)
	}
	seen := map[string]bool{}
	for i, sp := range f.Servers {
		if sp.Name == "" {
			return nil, fmt.Errorf("server #%d: name is empty", i)
		}
		if sp.URL == "" {
			return nil, fmt.Errorf("server %q: url is empty", sp.Name)
		}
		if seen[sp.Name] {
			return nil, fmt.Errorf("duplicate server name %q", sp.Name)
		}
		seen[sp.Name] = true
	}
	return f.Servers, nil
}

// ========== state ==========

type Alert struct {
	Rule     string  `json:"rule"`
	Severity string  `json:"severity"` // ok | warn | crit
	Message  string  `json:"msg"`
	Value    float64 `json:"value"`
}

type ServerSnapshot struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Up        bool      `json:"up"`
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
	Worst     string    `json:"worst"` // ok | warn | crit | down
	Alerts    []Alert   `json:"alerts"`
}

type ServersHub struct {
	mu        sync.RWMutex
	order     []string
	snapshots map[string]*ServerSnapshot
	lastFams  map[string]map[string]*dto.MetricFamily
	specs     map[string]ServerSpec
	http      *http.Client
}

func NewServersHub(specs []ServerSpec) *ServersHub {
	h := &ServersHub{
		snapshots: map[string]*ServerSnapshot{},
		lastFams:  map[string]map[string]*dto.MetricFamily{},
		specs:     map[string]ServerSpec{},
		http:      &http.Client{Timeout: 5 * time.Second},
	}
	for _, sp := range specs {
		h.order = append(h.order, sp.Name)
		h.specs[sp.Name] = sp
		h.snapshots[sp.Name] = &ServerSnapshot{Name: sp.Name, URL: sp.URL, Worst: "down"}
	}
	return h
}

// Start запускает поллер на каждый хост; sample раз в interval.
func (h *ServersHub) Start(ctx context.Context, interval time.Duration) {
	for _, name := range h.order {
		sp := h.specs[name]
		go h.pollLoop(ctx, sp, interval)
	}
}

func (h *ServersHub) pollLoop(ctx context.Context, sp ServerSpec, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	h.scrapeOnce(ctx, sp)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			h.scrapeOnce(ctx, sp)
		}
	}
}

func (h *ServersHub) scrapeOnce(ctx context.Context, sp ServerSpec) {
	snap := &ServerSnapshot{
		Name:      sp.Name,
		URL:       sp.URL,
		UpdatedAt: time.Now(),
	}
	families, err := scrapeNodeExporter(ctx, h.http, sp.URL)
	if err != nil {
		snap.Up = false
		snap.Error = err.Error()
		snap.Worst = "down"
		snap.Alerts = []Alert{{Rule: "NodeExporterDown", Severity: "crit", Message: err.Error()}}
		h.store(snap, nil)
		h.store(snap, nil)
		return
	}
	snap.Up = true
	snap.Alerts = evaluateRules(families)
	snap.Worst = worstSeverity(snap.Alerts)
	h.store(snap, families)
}

func (h *ServersHub) store(s *ServerSnapshot, fams map[string]*dto.MetricFamily) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.snapshots[s.Name] = s
	if fams != nil {
		h.lastFams[s.Name] = fams
	}
}

func (h *ServersHub) Snapshot() []*ServerSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*ServerSnapshot, 0, len(h.order))
	for _, n := range h.order {
		out = append(out, h.snapshots[n])
	}
	return out
}

// ========== scrape ==========

func scrapeNodeExporter(ctx context.Context, client *http.Client, baseURL string) (map[string]*dto.MetricFamily, error) {
	url := strings.TrimRight(baseURL, "/") + "/metrics"
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", string(expfmt.NewFormat(expfmt.TypeTextPlain)))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	return parser.TextToMetricFamilies(resp.Body)
}

// ========== rules ==========
//
// Пять правил, без rate (одиночный сэмпл): MemoryUsage, LoadAverage,
// DiskSpace, Inodes, ClockSkew. Down — отдельным маркером в scrapeOnce.

func evaluateRules(fams map[string]*dto.MetricFamily) []Alert {
	out := []Alert{}

	if a, ok := ruleMemoryUsage(fams); ok {
		out = append(out, a)
	}
	if a, ok := ruleLoadAverage(fams); ok {
		out = append(out, a)
	}
	out = append(out, ruleDiskSpace(fams)...)
	out = append(out, ruleInodes(fams)...)
	if a, ok := ruleClockSkew(fams); ok {
		out = append(out, a)
	}
	return out
}

func ruleMemoryUsage(fams map[string]*dto.MetricFamily) (Alert, bool) {
	avail := singleGauge(fams, "node_memory_MemAvailable_bytes")
	total := singleGauge(fams, "node_memory_MemTotal_bytes")
	if total <= 0 {
		return Alert{}, false
	}
	used := (1 - avail/total) * 100
	a := Alert{Rule: "HostHighMemoryUsage", Value: round1(used),
		Message: fmt.Sprintf("memory used %.1f%%", used), Severity: "ok"}
	switch {
	case used >= 95:
		a.Severity = "crit"
	case used >= 85:
		a.Severity = "warn"
	}
	return a, true
}

func ruleLoadAverage(fams map[string]*dto.MetricFamily) (Alert, bool) {
	load5 := singleGauge(fams, "node_load5")
	if load5 <= 0 {
		return Alert{}, false
	}
	cores := countCPU(fams)
	if cores <= 0 {
		cores = 1
	}
	per := load5 / cores
	a := Alert{Rule: "HostHighLoadAverage", Value: round1(per),
		Message:  fmt.Sprintf("load5/core = %.2f (load5=%.2f, cores=%.0f)", per, load5, cores),
		Severity: "ok"}
	switch {
	case per >= 2.0:
		a.Severity = "crit"
	case per >= 1.5:
		a.Severity = "warn"
	}
	return a, true
}

// ruleDiskSpace и ruleInodes отдают по алерту на каждую "плохую" фс.
func ruleDiskSpace(fams map[string]*dto.MetricFamily) []Alert {
	avail := fams["node_filesystem_avail_bytes"]
	size := fams["node_filesystem_size_bytes"]
	if avail == nil || size == nil {
		return nil
	}
	sizeByMnt := indexByLabel(size, "mountpoint", "fstype")
	out := []Alert{}
	worst := Alert{Rule: "HostOutOfDiskSpace", Severity: "ok",
		Message: "all filesystems > 15% free"}
	worstSeen := false
	for _, m := range avail.Metric {
		mnt := labelOf(m, "mountpoint")
		fstype := labelOf(m, "fstype")
		if isPseudoFS(fstype) {
			continue
		}
		key := mnt + "|" + fstype
		szm, ok := sizeByMnt[key]
		if !ok {
			continue
		}
		sz := gaugeValue(szm)
		av := gaugeValue(m)
		if sz <= 0 {
			continue
		}
		freePct := av / sz * 100
		if freePct >= 15 {
			continue
		}
		sev := "warn"
		if freePct < 5 {
			sev = "crit"
		}
		a := Alert{
			Rule:     "HostOutOfDiskSpace",
			Severity: sev,
			Value:    round1(freePct),
			Message:  fmt.Sprintf("%s (%s): %.1f%% free", mnt, fstype, freePct),
		}
		if !worstSeen || sevRank(sev) > sevRank(worst.Severity) {
			worst = a
			worstSeen = true
		}
		out = append(out, a)
	}
	if !worstSeen {
		return []Alert{worst}
	}
	// схлопываем в один — для UI хватит самого жёсткого, остальные в Message.
	if len(out) > 1 {
		others := []string{}
		for _, a := range out {
			if a.Message != worst.Message {
				others = append(others, a.Message)
			}
		}
		if len(others) > 0 {
			worst.Message += " (+" + fmt.Sprintf("%d", len(others)) + " more)"
		}
	}
	return []Alert{worst}
}

func ruleInodes(fams map[string]*dto.MetricFamily) []Alert {
	avail := fams["node_filesystem_files_free"]
	size := fams["node_filesystem_files"]
	if avail == nil || size == nil {
		return nil
	}
	sizeByMnt := indexByLabel(size, "mountpoint", "fstype")
	worst := Alert{Rule: "HostOutOfInodes", Severity: "ok",
		Message: "all filesystems > 15% inodes free"}
	any := false
	for _, m := range avail.Metric {
		mnt := labelOf(m, "mountpoint")
		fstype := labelOf(m, "fstype")
		if isPseudoFS(fstype) {
			continue
		}
		szm, ok := sizeByMnt[mnt+"|"+fstype]
		if !ok {
			continue
		}
		sz := gaugeValue(szm)
		av := gaugeValue(m)
		if sz <= 0 {
			continue
		}
		freePct := av / sz * 100
		if freePct >= 15 {
			continue
		}
		sev := "warn"
		if freePct < 5 {
			sev = "crit"
		}
		a := Alert{
			Rule:     "HostOutOfInodes",
			Severity: sev,
			Value:    round1(freePct),
			Message:  fmt.Sprintf("%s (%s): %.1f%% inodes free", mnt, fstype, freePct),
		}
		if !any || sevRank(sev) > sevRank(worst.Severity) {
			worst = a
			any = true
		}
	}
	return []Alert{worst}
}

func ruleClockSkew(fams map[string]*dto.MetricFamily) (Alert, bool) {
	// node_timex_offset_seconds: смещение системных часов относительно эталона.
	off := singleGauge(fams, "node_timex_offset_seconds")
	if off == 0 {
		// 0 при отсутствии метрики тоже даёт 0 — это нормально, NTP в норме.
		// Но если метрики нет вообще — не репортим.
		if _, ok := fams["node_timex_offset_seconds"]; !ok {
			return Alert{}, false
		}
	}
	abs := off
	if abs < 0 {
		abs = -abs
	}
	a := Alert{Rule: "HostClockSkew", Value: round1(off * 1000),
		Message:  fmt.Sprintf("ntp offset %.1f ms", off*1000),
		Severity: "ok"}
	switch {
	case abs >= 1.0:
		a.Severity = "crit" // > 1s
	case abs >= 0.05:
		a.Severity = "warn" // > 50ms
	}
	return a, true
}

// ========== helpers ==========

func singleGauge(fams map[string]*dto.MetricFamily, name string) float64 {
	f, ok := fams[name]
	if !ok || len(f.Metric) == 0 {
		return 0
	}
	return gaugeValue(f.Metric[0])
}

func gaugeValue(m *dto.Metric) float64 {
	if m.Gauge != nil {
		return m.Gauge.GetValue()
	}
	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	if m.Untyped != nil {
		return m.Untyped.GetValue()
	}
	return 0
}

func labelOf(m *dto.Metric, name string) string {
	for _, l := range m.Label {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func indexByLabel(f *dto.MetricFamily, k1, k2 string) map[string]*dto.Metric {
	out := map[string]*dto.Metric{}
	for _, m := range f.Metric {
		out[labelOf(m, k1)+"|"+labelOf(m, k2)] = m
	}
	return out
}

// countCPU = число ядер (по уникальным cpu-лейблам node_cpu_seconds_total).
func countCPU(fams map[string]*dto.MetricFamily) float64 {
	f, ok := fams["node_cpu_seconds_total"]
	if !ok {
		return 0
	}
	cpus := map[string]struct{}{}
	for _, m := range f.Metric {
		cpus[labelOf(m, "cpu")] = struct{}{}
	}
	return float64(len(cpus))
}

func isPseudoFS(fstype string) bool {
	switch fstype {
	case "tmpfs", "overlay", "squashfs", "devtmpfs", "proc", "sysfs",
		"cgroup", "cgroup2", "ramfs", "rootfs", "fuse", "nsfs", "tracefs",
		"debugfs", "configfs", "securityfs", "pstore", "bpf", "autofs",
		"mqueue", "hugetlbfs", "fusectl", "binfmt_misc":
		return true
	}
	return false
}

func sevRank(s string) int {
	switch s {
	case "crit":
		return 3
	case "warn":
		return 2
	case "ok":
		return 1
	}
	return 0
}

func worstSeverity(alerts []Alert) string {
	best := "ok"
	for _, a := range alerts {
		if sevRank(a.Severity) > sevRank(best) {
			best = a.Severity
		}
	}
	return best
}

// ========== detail ==========

type UnameInfo struct {
	Hostname string `json:"hostname"`
	Sysname  string `json:"sysname"`
	Release  string `json:"release"` // ядро
	Version  string `json:"version"`
	Machine  string `json:"machine"` // arch
}

type CPUInfo struct {
	Cores  int     `json:"cores"`
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type MemInfo struct {
	TotalBytes     float64 `json:"totalBytes"`
	AvailableBytes float64 `json:"availableBytes"`
	FreeBytes      float64 `json:"freeBytes"`
	BuffersBytes   float64 `json:"buffersBytes"`
	CachedBytes    float64 `json:"cachedBytes"`
	SwapTotalBytes float64 `json:"swapTotalBytes"`
	SwapFreeBytes  float64 `json:"swapFreeBytes"`
}

type FsInfo struct {
	Mountpoint    string  `json:"mountpoint"`
	Fstype        string  `json:"fstype"`
	SizeBytes     float64 `json:"sizeBytes"`
	AvailBytes    float64 `json:"availBytes"`
	UsedPct       float64 `json:"usedPct"`
	InodesTotal   float64 `json:"inodesTotal"`
	InodesFree    float64 `json:"inodesFree"`
	InodesUsedPct float64 `json:"inodesUsedPct"`
}

type NetInfo struct {
	Iface   string  `json:"iface"`
	RxBytes float64 `json:"rxBytes"`
	TxBytes float64 `json:"txBytes"`
}

type ServerDetail struct {
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	Up            bool      `json:"up"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Worst         string    `json:"worst"`
	Error         string    `json:"error,omitempty"`
	Uname         UnameInfo `json:"uname"`
	UptimeSeconds float64   `json:"uptimeSeconds"`
	BootTimeUnix  float64   `json:"bootTimeUnix"`
	CPU           CPUInfo   `json:"cpu"`
	Mem           MemInfo   `json:"mem"`
	Filesystems   []FsInfo  `json:"filesystems"`
	Networks      []NetInfo `json:"networks"`
	Alerts        []Alert   `json:"alerts"`
}

func buildDetail(snap *ServerSnapshot, fams map[string]*dto.MetricFamily) *ServerDetail {
	d := &ServerDetail{
		Name:      snap.Name,
		URL:       snap.URL,
		Up:        snap.Up,
		UpdatedAt: snap.UpdatedAt,
		Worst:     snap.Worst,
		Error:     snap.Error,
		Alerts:    snap.Alerts,
	}
	if fams == nil {
		return d
	}

	if u, ok := fams["node_uname_info"]; ok && len(u.Metric) > 0 {
		m := u.Metric[0]
		d.Uname = UnameInfo{
			Hostname: labelOf(m, "nodename"),
			Sysname:  labelOf(m, "sysname"),
			Release:  labelOf(m, "release"),
			Version:  labelOf(m, "version"),
			Machine:  labelOf(m, "machine"),
		}
	}

	now := singleGauge(fams, "node_time_seconds")
	boot := singleGauge(fams, "node_boot_time_seconds")
	if boot > 0 {
		d.BootTimeUnix = boot
		if now > 0 {
			d.UptimeSeconds = now - boot
		}
	}

	d.CPU = CPUInfo{
		Cores:  int(countCPU(fams)),
		Load1:  singleGauge(fams, "node_load1"),
		Load5:  singleGauge(fams, "node_load5"),
		Load15: singleGauge(fams, "node_load15"),
	}
	d.Mem = MemInfo{
		TotalBytes:     singleGauge(fams, "node_memory_MemTotal_bytes"),
		AvailableBytes: singleGauge(fams, "node_memory_MemAvailable_bytes"),
		FreeBytes:      singleGauge(fams, "node_memory_MemFree_bytes"),
		BuffersBytes:   singleGauge(fams, "node_memory_Buffers_bytes"),
		CachedBytes:    singleGauge(fams, "node_memory_Cached_bytes"),
		SwapTotalBytes: singleGauge(fams, "node_memory_SwapTotal_bytes"),
		SwapFreeBytes:  singleGauge(fams, "node_memory_SwapFree_bytes"),
	}

	// filesystems
	if avail, ok := fams["node_filesystem_avail_bytes"]; ok {
		size := fams["node_filesystem_size_bytes"]
		ifree := fams["node_filesystem_files_free"]
		itotal := fams["node_filesystem_files"]
		var sizeIdx, ifreeIdx, itotalIdx map[string]*dto.Metric
		if size != nil {
			sizeIdx = indexByLabel(size, "mountpoint", "fstype")
		}
		if ifree != nil {
			ifreeIdx = indexByLabel(ifree, "mountpoint", "fstype")
		}
		if itotal != nil {
			itotalIdx = indexByLabel(itotal, "mountpoint", "fstype")
		}
		for _, m := range avail.Metric {
			mnt := labelOf(m, "mountpoint")
			fst := labelOf(m, "fstype")
			if isPseudoFS(fst) {
				continue
			}
			key := mnt + "|" + fst
			fs := FsInfo{Mountpoint: mnt, Fstype: fst, AvailBytes: gaugeValue(m)}
			if sm, ok := sizeIdx[key]; ok {
				fs.SizeBytes = gaugeValue(sm)
				if fs.SizeBytes > 0 {
					fs.UsedPct = round1((1 - fs.AvailBytes/fs.SizeBytes) * 100)
				}
			}
			if im, ok := itotalIdx[key]; ok {
				fs.InodesTotal = gaugeValue(im)
			}
			if im, ok := ifreeIdx[key]; ok {
				fs.InodesFree = gaugeValue(im)
				if fs.InodesTotal > 0 {
					fs.InodesUsedPct = round1((1 - fs.InodesFree/fs.InodesTotal) * 100)
				}
			}
			d.Filesystems = append(d.Filesystems, fs)
		}
		sort.Slice(d.Filesystems, func(i, j int) bool {
			return d.Filesystems[i].Mountpoint < d.Filesystems[j].Mountpoint
		})
	}

	// network
	if rx, ok := fams["node_network_receive_bytes_total"]; ok {
		var txIdx map[string]*dto.Metric
		if tx := fams["node_network_transmit_bytes_total"]; tx != nil {
			txIdx = map[string]*dto.Metric{}
			for _, m := range tx.Metric {
				txIdx[labelOf(m, "device")] = m
			}
		}
		for _, m := range rx.Metric {
			iface := labelOf(m, "device")
			if iface == "lo" {
				continue
			}
			n := NetInfo{Iface: iface, RxBytes: gaugeValue(m)}
			if tm, ok := txIdx[iface]; ok {
				n.TxBytes = gaugeValue(tm)
			}
			d.Networks = append(d.Networks, n)
		}
		sort.Slice(d.Networks, func(i, j int) bool { return d.Networks[i].Iface < d.Networks[j].Iface })
	}

	return d
}

// ========== handler ==========

func (h *ServersHub) handleList(w http.ResponseWriter, _ *http.Request) {
	snaps := h.Snapshot()
	// детерминированный порядок у алертов
	for _, s := range snaps {
		sort.Slice(s.Alerts, func(i, j int) bool {
			if s.Alerts[i].Severity != s.Alerts[j].Severity {
				return sevRank(s.Alerts[i].Severity) > sevRank(s.Alerts[j].Severity)
			}
			return s.Alerts[i].Rule < s.Alerts[j].Rule
		})
	}
	writeJSON(w, snaps)
}

func (h *ServersHub) handleDetail(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("missing ?name="))
		return
	}
	h.mu.RLock()
	snap, ok := h.snapshots[name]
	fams := h.lastFams[name]
	h.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("unknown server %q", name))
		return
	}
	writeJSON(w, buildDetail(snap, fams))
}
