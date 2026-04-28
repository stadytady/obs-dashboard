package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mustReq — общий helper: сконструировать httptest-запрос или провалить тест.
func mustReq(t *testing.T, path string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, path, nil)
}

func TestRound1(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0, 0},
		{1.0, 1.0},
		{1.04, 1.0},
		{1.05, 1.1}, // round half up
		{1.95, 2.0},
		{99.99, 100.0},
		{42.123456, 42.1},
	}
	for _, c := range cases {
		if got := round1(c.in); got != c.want {
			t.Errorf("round1(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPtrInt32(t *testing.T) {
	if got := ptrInt32(nil); got != 0 {
		t.Errorf("ptrInt32(nil) = %d, want 0", got)
	}
	v := int32(42)
	if got := ptrInt32(&v); got != 42 {
		t.Errorf("ptrInt32(&42) = %d, want 42", got)
	}
}

func TestHumanAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, "—"},
		{"5min", now.Add(-5 * time.Minute), "5m"},
		{"3h", now.Add(-3 * time.Hour), "3h"},
		{"5d", now.Add(-5 * 24 * time.Hour), "5d"},
	}
	for _, c := range cases {
		if got := humanAge(c.t); got != c.want {
			t.Errorf("%s: humanAge = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHumanAgeShort(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, "—"},
		{"now", now.Add(-30 * time.Second), "now"},
		{"5min", now.Add(-5 * time.Minute), "5m"},
		{"3h", now.Add(-3 * time.Hour), "3h"},
		{"5d", now.Add(-5 * 24 * time.Hour), "5d"},
	}
	for _, c := range cases {
		if got := humanAgeShort(c.t); got != c.want {
			t.Errorf("%s: humanAgeShort = %q, want %q", c.name, got, c.want)
		}
	}
}
