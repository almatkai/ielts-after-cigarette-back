// Package observability exposes operational metrics on a separate private
// listener. Never register its handler on the public API router.
package observability

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
}

// New registers durable job metrics when pool is set. speechPipeline reports
// the speaking and transcription queues, which only have a consumer when
// SPEECH_ENABLED is on; otherwise their rows are never processed.
func New(pool *pgxpool.Pool, speechPipeline bool) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "iac_http_requests_total", Help: "Completed API requests."}, []string{"method", "route", "status"}),
		latency:  prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "iac_http_duration_seconds", Help: "API request duration.", Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300}}, []string{"method", "route"}),
	}
	m.registry.MustRegister(m.requests, m.latency, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	if pool != nil {
		m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "iac_db_connections", Help: "Total pgx connections."}, func() float64 { return float64(pool.Stat().TotalConns()) }))
		m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "iac_db_connections_acquired", Help: "Busy pgx connections."}, func() float64 { return float64(pool.Stat().AcquiredConns()) }))
		m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "iac_db_connections_max", Help: "Configured pgx connection limit."}, func() float64 { return float64(pool.Stat().MaxConns()) }))
		kinds := []string{"writing"}
		if speechPipeline {
			kinds = append(kinds, "speaking", "transcription")
		}
		m.registry.MustRegister(&jobCollector{pool: pool, kinds: kinds})
	}
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Install inside chi, before recovery, so even recovered panics are counted.
// Route templates (not URLs/user IDs) keep cardinality bounded.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(recorder, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		method := r.Method
		switch method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		default:
			method = "OTHER"
		}
		status := recorder.Status()
		if status == 0 {
			status = http.StatusOK
		}
		m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
		m.latency.WithLabelValues(method, route).Observe(time.Since(started).Seconds())
	})
}

var (
	jobCount    = prometheus.NewDesc("iac_jobs", "Durable PostgreSQL jobs by kind and state (Redis contains wake-up hints only).", []string{"kind", "status"}, nil)
	jobAge      = prometheus.NewDesc("iac_jobs_oldest_active_seconds", "Age of the oldest queued or processing durable job.", []string{"kind"}, nil)
	jobScrapeUp = prometheus.NewDesc("iac_jobs_scrape_up", "Whether the durable job query succeeded.", nil, nil)
)

type jobCollector struct {
	pool  *pgxpool.Pool
	kinds []string
}

func (c *jobCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- jobCount
	ch <- jobAge
	ch <- jobScrapeUp
}
func (c *jobCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := c.pool.Query(ctx, `
		SELECT kind, status, count(*), COALESCE(EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - min(created_at))),0)::double precision
		FROM (
			SELECT 'writing' AS kind, status, created_at FROM writing_assessment_jobs WHERE status IN ('QUEUED','PROCESSING','FAILED')
			UNION ALL SELECT 'speaking', status, created_at FROM speaking_assessment_jobs WHERE status IN ('QUEUED','PROCESSING','FAILED')
			UNION ALL SELECT 'transcription', status, created_at FROM speaking_transcriptions WHERE status IN ('QUEUED','PROCESSING','FAILED')
		) jobs GROUP BY kind,status`)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(jobScrapeUp, prometheus.GaugeValue, 0)
		return
	}
	defer rows.Close()
	type state struct {
		kind, status string
		count        float64
		age          float64
	}
	var states []state
	for rows.Next() {
		var s state
		if err := rows.Scan(&s.kind, &s.status, &s.count, &s.age); err != nil {
			ch <- prometheus.MustNewConstMetric(jobScrapeUp, prometheus.GaugeValue, 0)
			return
		}
		states = append(states, s)
	}
	if rows.Err() != nil {
		ch <- prometheus.MustNewConstMetric(jobScrapeUp, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(jobScrapeUp, prometheus.GaugeValue, 1)
	for _, kind := range c.kinds {
		age := 0.0
		for _, status := range []string{"QUEUED", "PROCESSING", "FAILED"} {
			count := 0.0
			for _, s := range states {
				if s.kind == kind && s.status == status {
					count = s.count
					if status != "FAILED" && s.age > age {
						age = s.age
					}
				}
			}
			ch <- prometheus.MustNewConstMetric(jobCount, prometheus.GaugeValue, count, kind, status)
		}
		ch <- prometheus.MustNewConstMetric(jobAge, prometheus.GaugeValue, age, kind)
	}
}
