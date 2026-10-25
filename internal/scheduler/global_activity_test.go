package scheduler

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

func globalActivityClient(srv *httptest.Server) *upstream.Client {
	return &upstream.Client{
		HTTP:              srv.Client(),
		GlobalEnabled:     true,
		BillingBaseCN:     srv.URL,
		BillingBaseGlobal: srv.URL,
	}
}

func TestGlobalActivityAlreadyLitSkipsReport(t *testing.T) {
	withGlobalSwitch(t)
	var heatmapHits, reportHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/activity/growth/heatmap":
			heatmapHits.Add(1)
			w.Write([]byte(`{"code":0,"data":{"cells":[{"date":"2026-10-24","score":0},{"date":"2026-10-25","score":2}]}}`))
		case "/v2/report":
			reportHits.Add(1)
			w.Write([]byte(`{"code":0,"msg":"OK"}`))
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	p := pool.New("")
	p.Add(globalAccount(t, "g1"))
	s := New(Config{Pool: p, Upstream: globalActivityClient(srv)})

	results := s.RunGlobalActivityNow()
	if len(results) != 1 {
		t.Fatalf("results=%d want 1", len(results))
	}
	if results[0].Status != globalActivityAlreadyLit || !results[0].Lit || results[0].Score != 2 {
		t.Errorf("result=%+v want already-lit score=2", results[0])
	}
	if heatmapHits.Load() != 1 {
		t.Errorf("heatmap hits=%d want 1", heatmapHits.Load())
	}
	if reportHits.Load() != 0 {
		t.Errorf("report hits=%d want 0 when already lit", reportHits.Load())
	}
}

func TestGlobalActivityZeroScoreReportsOncePerDayInProcess(t *testing.T) {
	withGlobalSwitch(t)
	fastActivity(t)
	var heatmapHits, reportHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/activity/growth/heatmap":
			heatmapHits.Add(1)
			w.Write([]byte(`{"code":0,"data":{"cells":[{"date":"2026-10-25","score":0}]}}`))
		case "/v2/report":
			reportHits.Add(1)
			w.Write([]byte(`{"code":0,"msg":"OK"}`))
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	p := pool.New("")
	p.Add(globalAccount(t, "g1"))
	s := New(Config{Pool: p, Upstream: globalActivityClient(srv)})

	first := s.RunGlobalActivityNow()
	if len(first) != 1 || first[0].Status != globalActivityReported || first[0].Lit {
		t.Fatalf("first result=%+v want reported with heatmap still pending", first)
	}
	second, err := s.RunGlobalActivityForUID("g1")
	if err != nil {
		t.Fatalf("manual rerun: %v", err)
	}
	if second.Status != globalActivityReported || second.Detail == "" {
		t.Errorf("second result=%+v want local idempotency detail", second)
	}
	if reportHits.Load() != 1 {
		t.Errorf("report hits=%d want exactly 1 despite heatmap lag", reportHits.Load())
	}
	if heatmapHits.Load() != 2 {
		t.Errorf("heatmap hits=%d want 2 (one read per run)", heatmapHits.Load())
	}
}

func TestGlobalActivityHeatmapFailureNeverReports(t *testing.T) {
	withGlobalSwitch(t)
	var reportHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/report" {
			reportHits.Add(1)
			w.Write([]byte(`{"code":0}`))
			return
		}
		http.Error(w, "upstream down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := pool.New("")
	p.Add(globalAccount(t, "g1"))
	s := New(Config{Pool: p, Upstream: globalActivityClient(srv)})

	results := s.RunGlobalActivityNow()
	if len(results) != 1 || results[0].Status != globalActivityFailed {
		t.Fatalf("results=%+v want one failed row", results)
	}
	if reportHits.Load() != 0 {
		t.Errorf("report hits=%d want 0 on heatmap failure", reportHits.Load())
	}
}

func TestGlobalActivityReportFailureNotRetried(t *testing.T) {
	withGlobalSwitch(t)
	var reportHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/activity/growth/heatmap":
			w.Write([]byte(`{"code":0,"data":{"cells":[{"date":"2026-10-25","score":0}]}}`))
		case "/v2/report":
			reportHits.Add(1)
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	p := pool.New("")
	p.Add(globalAccount(t, "g1"))
	s := New(Config{Pool: p, Upstream: globalActivityClient(srv)})

	results := s.RunGlobalActivityNow()
	if len(results) != 1 || results[0].Status != globalActivityFailed {
		t.Fatalf("results=%+v want one failed row", results)
	}
	if reportHits.Load() != 1 {
		t.Errorf("report hits=%d want 1, no same-run retry", reportHits.Load())
	}
}

func TestNextWakeGlobalActivityIndependent(t *testing.T) {
	s := New(Config{
		CheckinHours:        []int{21},
		TravelHours:         []int{9},
		ActivityHours:       []int{10},
		GlobalActivityHours: []int{8, 12, 16, 20},
		KeepaliveHours:      []int{22},
	})
	at, kinds := s.nextWake(time.Date(2026, 10, 25, 7, 30, 0, 0, time.Local))
	if want := time.Date(2026, 10, 25, 8, 0, 0, 0, time.Local); !at.Equal(want) {
		t.Errorf("next=%v want %v", at, want)
	}
	if len(kinds) != 1 || kinds[0] != taskGlobalActivity {
		t.Errorf("kinds=%v want [global-activity]", kinds)
	}
}
