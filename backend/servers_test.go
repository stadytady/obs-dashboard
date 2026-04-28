package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestSevRank(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"crit", 3}, {"warn", 2}, {"ok", 1}, {"", 0}, {"unknown-sev", 0},
	}
	for _, c := range cases {
		if got := sevRank(c.in); got != c.want {
			t.Errorf("sevRank(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestWorstSeverity(t *testing.T) {
	cases := []struct {
		name   string
		alerts []Alert
		want   string
	}{
		{"empty -> ok", nil, "ok"},
		{"all ok", []Alert{{Severity: "ok"}, {Severity: "ok"}}, "ok"},
		{"warn beats ok", []Alert{{Severity: "ok"}, {Severity: "warn"}}, "warn"},
		{"crit beats warn", []Alert{{Severity: "warn"}, {Severity: "crit"}, {Severity: "ok"}}, "crit"},
	}
	for _, c := range cases {
		if got := worstSeverity(c.alerts); got != c.want {
			t.Errorf("%s: worstSeverity = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsPseudoFS(t *testing.T) {
	pseudo := []string{"tmpfs", "overlay", "proc", "sysfs", "cgroup2", "fuse"}
	real := []string{"ext4", "xfs", "btrfs", "zfs", "vfat", ""}
	for _, fs := range pseudo {
		if !isPseudoFS(fs) {
			t.Errorf("%q should be pseudo", fs)
		}
	}
	for _, fs := range real {
		if isPseudoFS(fs) {
			t.Errorf("%q should NOT be pseudo", fs)
		}
	}
}

// fam — мини-конструктор MetricFamily для тестов.
func fam(name string, samples ...gaugeSample) *dto.MetricFamily {
	mt := dto.MetricType_GAUGE
	f := &dto.MetricFamily{Name: &name, Type: &mt}
	for _, s := range samples {
		s := s
		m := &dto.Metric{Gauge: &dto.Gauge{Value: &s.value}}
		for k, v := range s.labels {
			k, v := k, v
			m.Label = append(m.Label, &dto.LabelPair{Name: &k, Value: &v})
		}
		f.Metric = append(f.Metric, m)
	}
	return f
}

type gaugeSample struct {
	value  float64
	labels map[string]string
}

func TestRuleMemoryUsage(t *testing.T) {
	cases := []struct {
		name      string
		total     float64
		available float64
		wantSev   string
	}{
		{"50% used = ok", 1000, 500, "ok"},
		{"85% boundary = warn", 1000, 150, "warn"},
		{"94% used = warn", 1000, 60, "warn"},
		{"95% boundary = crit", 1000, 50, "crit"},
		{"99% used = crit", 1000, 10, "crit"},
	}
	for _, c := range cases {
		fams := map[string]*dto.MetricFamily{
			"node_memory_MemTotal_bytes":     fam("node_memory_MemTotal_bytes", gaugeSample{value: c.total}),
			"node_memory_MemAvailable_bytes": fam("node_memory_MemAvailable_bytes", gaugeSample{value: c.available}),
		}
		a, ok := ruleMemoryUsage(fams)
		if !ok {
			t.Errorf("%s: rule didn't fire", c.name)
			continue
		}
		if a.Severity != c.wantSev {
			t.Errorf("%s: sev=%q, want %q (used=%.1f%%)", c.name, a.Severity, c.wantSev, a.Value)
		}
	}

	// total=0 → правило не должно стрелять (защита от деления на ноль).
	if _, ok := ruleMemoryUsage(map[string]*dto.MetricFamily{}); ok {
		t.Error("rule fired with no metrics")
	}
}

func TestRuleLoadAverage(t *testing.T) {
	cpus := func(n int) *dto.MetricFamily {
		samples := make([]gaugeSample, n)
		for i := 0; i < n; i++ {
			samples[i] = gaugeSample{value: 1, labels: map[string]string{"cpu": string(rune('0' + i))}}
		}
		return fam("node_cpu_seconds_total", samples...)
	}
	cases := []struct {
		name    string
		load5   float64
		cores   int
		wantSev string
	}{
		{"low load", 1.0, 4, "ok"},
		{"warn boundary 1.5/core", 6.0, 4, "warn"},
		{"crit boundary 2.0/core", 8.0, 4, "crit"},
		{"high crit", 10.0, 4, "crit"},
	}
	for _, c := range cases {
		fams := map[string]*dto.MetricFamily{
			"node_load5":             fam("node_load5", gaugeSample{value: c.load5}),
			"node_cpu_seconds_total": cpus(c.cores),
		}
		a, ok := ruleLoadAverage(fams)
		if !ok {
			t.Errorf("%s: rule didn't fire", c.name)
			continue
		}
		if a.Severity != c.wantSev {
			t.Errorf("%s: sev=%q, want %q", c.name, a.Severity, c.wantSev)
		}
	}
}

func TestRuleDiskSpace(t *testing.T) {
	mk := func(mp, fs string, size, avail float64) (*dto.MetricFamily, *dto.MetricFamily) {
		labels := map[string]string{"mountpoint": mp, "fstype": fs}
		return fam("node_filesystem_avail_bytes", gaugeSample{value: avail, labels: labels}),
			fam("node_filesystem_size_bytes", gaugeSample{value: size, labels: labels})
	}

	t.Run("plenty free = ok", func(t *testing.T) {
		availF, sizeF := mk("/", "ext4", 100, 80) // 80% free
		fams := map[string]*dto.MetricFamily{
			"node_filesystem_avail_bytes": availF,
			"node_filesystem_size_bytes":  sizeF,
		}
		alerts := ruleDiskSpace(fams)
		if len(alerts) != 1 || alerts[0].Severity != "ok" {
			t.Errorf("got %+v, want single ok alert", alerts)
		}
	})

	t.Run("10% free = warn", func(t *testing.T) {
		availF, sizeF := mk("/", "ext4", 100, 10)
		fams := map[string]*dto.MetricFamily{
			"node_filesystem_avail_bytes": availF,
			"node_filesystem_size_bytes":  sizeF,
		}
		alerts := ruleDiskSpace(fams)
		if len(alerts) != 1 || alerts[0].Severity != "warn" {
			t.Errorf("got %+v, want warn", alerts)
		}
	})

	t.Run("3% free = crit", func(t *testing.T) {
		availF, sizeF := mk("/", "ext4", 100, 3)
		fams := map[string]*dto.MetricFamily{
			"node_filesystem_avail_bytes": availF,
			"node_filesystem_size_bytes":  sizeF,
		}
		alerts := ruleDiskSpace(fams)
		if len(alerts) != 1 || alerts[0].Severity != "crit" {
			t.Errorf("got %+v, want crit", alerts)
		}
	})

	t.Run("tmpfs ignored", func(t *testing.T) {
		availF, sizeF := mk("/run", "tmpfs", 100, 1) // 1% free, но pseudo-fs
		fams := map[string]*dto.MetricFamily{
			"node_filesystem_avail_bytes": availF,
			"node_filesystem_size_bytes":  sizeF,
		}
		alerts := ruleDiskSpace(fams)
		if len(alerts) != 1 || alerts[0].Severity != "ok" {
			t.Errorf("got %+v, want single ok alert (tmpfs should be skipped)", alerts)
		}
	})
}

func TestRuleClockSkew(t *testing.T) {
	cases := []struct {
		name    string
		offset  float64 // секунды
		wantSev string
	}{
		{"on time", 0.001, "ok"}, // 1 ms
		{"warn boundary 50ms", 0.05, "warn"},
		{"between thresholds", 0.5, "warn"},
		{"crit boundary 1s", 1.0, "crit"},
		{"big skew", 5.0, "crit"},
		{"negative big skew", -5.0, "crit"},
	}
	for _, c := range cases {
		fams := map[string]*dto.MetricFamily{
			"node_timex_offset_seconds": fam("node_timex_offset_seconds", gaugeSample{value: c.offset}),
		}
		a, ok := ruleClockSkew(fams)
		if !ok {
			t.Errorf("%s: rule didn't fire", c.name)
			continue
		}
		if a.Severity != c.wantSev {
			t.Errorf("%s: sev=%q, want %q (offset=%v)", c.name, a.Severity, c.wantSev, c.offset)
		}
	}

	// Метрики нет вообще → правило молчит.
	if _, ok := ruleClockSkew(map[string]*dto.MetricFamily{}); ok {
		t.Error("rule fired without metric present")
	}
}

func TestScrapeNodeExporter(t *testing.T) {
	// Минимальный валидный node-exporter ответ.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(`# HELP node_load5 5m load avg
# TYPE node_load5 gauge
node_load5 0.42
# HELP node_memory_MemTotal_bytes Memory total
# TYPE node_memory_MemTotal_bytes gauge
node_memory_MemTotal_bytes 8.589934592e+09
`))
	}))
	defer srv.Close()

	fams, err := scrapeNodeExporter(context.Background(), &http.Client{}, srv.URL)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if singleGauge(fams, "node_load5") != 0.42 {
		t.Errorf("node_load5 = %v, want 0.42", singleGauge(fams, "node_load5"))
	}
	if singleGauge(fams, "node_memory_MemTotal_bytes") != 8589934592 {
		t.Errorf("memTotal not parsed correctly")
	}
}

func TestScrapeNodeExporterError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("nope"))
	}))
	defer srv.Close()

	_, err := scrapeNodeExporter(context.Background(), &http.Client{}, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("want 503 error, got %v", err)
	}
}
