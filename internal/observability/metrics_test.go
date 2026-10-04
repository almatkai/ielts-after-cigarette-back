package observability

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/go-chi/chi/v5"
)

func TestMetricsUseTemplatesAndCountRecoveredPanics(t *testing.T) {
	metrics := New(nil)
	r := chi.NewRouter()
	r.Use(metrics.Middleware)
	r.Use(httpx.Recover(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.Get("/attempts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if chi.URLParam(r, "id") == "panic" {
			panic("test")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	for _, path := range []string{"/attempts/student-a", "/attempts/student-b", "/attempts/panic", "/unknown-a", "/unknown-b"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, family := range families {
		if family.GetName() != "iac_http_requests_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["route"] != "/attempts/{id}" && labels["route"] != "unmatched" {
				t.Fatalf("unbounded route label: %v", labels)
			}
			if labels["status"] == "500" && metric.GetCounter().GetValue() == 1 {
				found++
			}
		}
		if len(family.Metric) != 3 {
			t.Fatalf("expected success/panic/unmatched series, got %d", len(family.Metric))
		}
	}
	if found != 1 {
		t.Fatal("panic was not counted as 500")
	}
}

func TestDurableJobCollectorOnMigratedDatabase(t *testing.T) {
	metrics := New(testdb.Open(t))
	families, err := metrics.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	up, jobs := false, 0
	for _, family := range families {
		if family.GetName() == "iac_jobs_scrape_up" {
			up = family.Metric[0].GetGauge().GetValue() == 1
		}
		if family.GetName() == "iac_jobs" {
			jobs = len(family.Metric)
		}
	}
	if !up || jobs != 9 {
		t.Fatalf("collector failed or omitted empty queues: up=%v series=%d", up, jobs)
	}
}
