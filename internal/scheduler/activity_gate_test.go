// activity_gate_test.go 钉住 CN 活跃上报的「先读热力图、未点亮才上报」门禁：
// 已点亮 → 不发；未点亮 → 发一次；热力图读失败 → 退回盲上报（CN 只有这一条
// 点亮路径，漏报等于断连登）；同一天多时点 → 进程内去重，不重复发。
package scheduler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// heatmapStub 固定返回一张「今天」的热力图；heatStatus 非 200 时模拟读失败。
type heatmapStub struct {
	score      atomic.Int32
	heatStatus atomic.Int32
	reports    atomic.Int32
}

func (s *heatmapStub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/activity/growth/heatmap":
			if code := int(s.heatStatus.Load()); code != 0 && code != http.StatusOK {
				w.WriteHeader(code)
				w.Write([]byte(`boom`))
				return
			}
			score := s.score.Load()
			w.Write([]byte(`{"code":0,"msg":"OK","data":{"cells":[` +
				`{"date":"2026-10-31","score":0},{"date":"2026-11-01","score":` +
				strconv.Itoa(int(score)) + `}]}}`))
		case "/v2/report":
			s.reports.Add(1)
			w.Write([]byte(`{"code":0,"msg":"OK"}`))
		default:
			// streak 自检等旁路请求：返回空 data 即可，不影响断言。
			w.Write([]byte(`{"code":0,"data":{}}`))
		}
	})
}

func newGateScheduler(t *testing.T, stub *heatmapStub) *Scheduler {
	t.Helper()
	fastActivity(t)
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	p := pool.New("")
	p.Add(&auth.Auth{UID: "u1", AccessToken: "at", RefreshToken: "rt", ExpiresAt: 9999999999})
	up := &upstream.Client{HTTP: srv.Client(), ChatBaseCN: srv.URL, BillingBaseCN: srv.URL}
	return New(Config{Pool: p, Upstream: up})
}

// TestActivitySkipsWhenHeatmapLit 今日已点亮 → 一次上报都不发。
func TestActivitySkipsWhenHeatmapLit(t *testing.T) {
	stub := &heatmapStub{}
	stub.score.Store(2)
	s := newGateScheduler(t, stub)

	s.RunActivityNow()

	if n := stub.reports.Load(); n != 0 {
		t.Errorf("report calls=%d want 0（已点亮应跳过）", n)
	}
}

// TestActivityReportsWhenHeatmapUnlit 今日未点亮 → 发一次上报。
func TestActivityReportsWhenHeatmapUnlit(t *testing.T) {
	stub := &heatmapStub{}
	stub.score.Store(0)
	s := newGateScheduler(t, stub)

	s.RunActivityNow()

	if n := stub.reports.Load(); n != 1 {
		t.Errorf("report calls=%d want 1（未点亮应补一次）", n)
	}
}

// TestActivityFallsBackWhenHeatmapFails 热力图读失败 → 退回盲上报（CN 口径）。
func TestActivityFallsBackWhenHeatmapFails(t *testing.T) {
	stub := &heatmapStub{}
	stub.heatStatus.Store(http.StatusInternalServerError)
	s := newGateScheduler(t, stub)

	s.RunActivityNow()

	if n := stub.reports.Load(); n != 1 {
		t.Errorf("report calls=%d want 1（读失败应退回盲上报）", n)
	}
}

// TestActivitySameDayNotReportedTwice 同一自然日多时点（10 点 + 21 点）只发一次：
// 热力图聚合有延迟，第二次读到的可能仍是 score=0，靠进程内当日记录去重。
func TestActivitySameDayNotReportedTwice(t *testing.T) {
	stub := &heatmapStub{}
	stub.score.Store(0)
	s := newGateScheduler(t, stub)

	s.RunActivityNow()
	s.RunActivityNow() // 模拟第二个时点

	if n := stub.reports.Load(); n != 1 {
		t.Errorf("report calls=%d want 1（同日多时点应去重）", n)
	}
}
