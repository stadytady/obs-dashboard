package main

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEventTime(t *testing.T) {
	last := time.Now().Add(-1 * time.Hour)
	event := time.Now().Add(-2 * time.Hour)
	first := time.Now().Add(-3 * time.Hour)
	creation := time.Now().Add(-4 * time.Hour)

	cases := []struct {
		name string
		ev   corev1.Event
		want time.Time
	}{
		{
			name: "LastTimestamp wins",
			ev: corev1.Event{
				LastTimestamp: metav1.NewTime(last),
				EventTime:     metav1.NewMicroTime(event),
				ObjectMeta:    metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(creation)},
			},
			want: last,
		},
		{
			name: "EventTime when LastTimestamp empty (events.k8s.io/v1)",
			ev: corev1.Event{
				EventTime:      metav1.NewMicroTime(event),
				FirstTimestamp: metav1.NewTime(first),
				ObjectMeta:     metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(creation)},
			},
			want: event,
		},
		{
			name: "FirstTimestamp when LastTimestamp+EventTime empty",
			ev: corev1.Event{
				FirstTimestamp: metav1.NewTime(first),
				ObjectMeta:     metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(creation)},
			},
			want: first,
		},
		{
			name: "CreationTimestamp last resort",
			ev: corev1.Event{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(creation)},
			},
			want: creation,
		},
	}
	for _, c := range cases {
		got := eventTime(c.ev)
		if !got.Equal(c.want) {
			t.Errorf("%s: eventTime = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNodeRole(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"no role labels = worker", map[string]string{}, "worker"},
		{"single master role", map[string]string{"node-role.kubernetes.io/master": ""}, "master"},
		{"control-plane + etcd sorted", map[string]string{
			"node-role.kubernetes.io/etcd":          "",
			"node-role.kubernetes.io/control-plane": "",
		}, "control-plane,etcd"},
		{"empty role suffix ignored", map[string]string{"node-role.kubernetes.io/": ""}, "worker"},
		{"non-role labels ignored", map[string]string{"foo/bar": "baz"}, "worker"},
	}
	for _, c := range cases {
		n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: c.labels}}
		if got := nodeRole(n); got != c.want {
			t.Errorf("%s: nodeRole = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestNodeStatus(t *testing.T) {
	cases := []struct {
		name       string
		conditions []corev1.NodeCondition
		want       string
	}{
		{"ready true", []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		}, "ready"},
		{"ready false", []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
		}, "err"},
		{"ready unknown", []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionUnknown},
		}, "err"},
		{"no ready condition", []corev1.NodeCondition{
			{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
		}, "err"},
		{"empty conditions", nil, "err"},
	}
	for _, c := range cases {
		n := &corev1.Node{Status: corev1.NodeStatus{Conditions: c.conditions}}
		if got := nodeStatus(n); got != c.want {
			t.Errorf("%s: nodeStatus = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSeriesBuffer(t *testing.T) {
	b := &SeriesBuffer{cap: 3}
	for i := 1; i <= 5; i++ {
		b.Push(SeriesPoint{TS: int64(i), CPU: float64(i), Mem: float64(i)})
	}
	snap := b.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len = %d, want 3 (cap)", len(snap))
	}
	if snap[0].TS != 3 || snap[2].TS != 5 {
		t.Errorf("oldest=%d newest=%d, want 3 and 5", snap[0].TS, snap[2].TS)
	}
	// Snapshot must be a copy — мутация не должна задеть буфер.
	snap[0].CPU = 999
	snap2 := b.Snapshot()
	if snap2[0].CPU == 999 {
		t.Error("Snapshot returned shared slice, expected copy")
	}
}

func TestMultiClusterPick(t *testing.T) {
	mc := &MultiCluster{
		clusters: map[string]*Cluster{
			"a": {Name: "a"},
			"b": {Name: "b"},
		},
		order: []string{"a", "b"},
	}
	cases := []struct {
		query   string
		want    string
		wantErr bool
	}{
		{"", "a", false},           // default = first
		{"?cluster=b", "b", false}, // explicit
		{"?cluster=a", "a", false}, // explicit
		{"?cluster=zzz", "", true}, // unknown
		{"?cluster=", "a", false},  // empty value = default
		{"?other=foo", "a", false}, // unrelated param
	}
	for _, c := range cases {
		req := mustReq(t, "/api/x"+c.query)
		got, err := mc.pick(req)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: want error, got %v", c.query, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err %v", c.query, err)
			continue
		}
		if got.Name != c.want {
			t.Errorf("%q: name = %q, want %q", c.query, got.Name, c.want)
		}
	}

	// Edge case: пустой реестр должен вернуть ошибку, а не паниковать.
	empty := &MultiCluster{clusters: map[string]*Cluster{}}
	if _, err := empty.pick(mustReq(t, "/api/x")); err == nil {
		t.Error("empty registry: want error, got nil")
	}
}
