package upstream

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

func globalGrowthTestAccount(t *testing.T) *auth.Auth {
	t.Helper()
	a := &auth.Auth{UID: "global-1", AccessToken: "at"}
	if _, err := auth.BackfillRealmFor(a, "global"); err != nil {
		t.Fatalf("BackfillRealmFor(global): %v", err)
	}
	return a
}

// 最后一个 cell 是 API 口径的“今天”；同时确认 Global 只走 global billing base。
func TestGrowthHeatmapTodayUsesLastCellAndGlobalBase(t *testing.T) {
	var cnHits, globalHits atomic.Int32
	cnSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cnHits.Add(1)
		http.Error(w, "wrong realm", http.StatusTeapot)
	}))
	defer cnSrv.Close()
	globalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		globalHits.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method=%s want GET", r.Method)
		}
		if r.URL.Path != growthHeatmapPath {
			t.Errorf("path=%s want %s", r.URL.Path, growthHeatmapPath)
		}
		w.Write([]byte(`{"code":0,"msg":"OK","data":{"cells":[{"date":"2026-10-23","score":2},{"date":"2026-10-24","score":0},{"date":"2026-10-25","score":4}]}}`))
	}))
	defer globalSrv.Close()

	c := &Client{
		HTTP:              globalSrv.Client(),
		GlobalEnabled:     true,
		BillingBaseCN:     cnSrv.URL,
		BillingBaseGlobal: globalSrv.URL,
	}
	day, err := c.GrowthHeatmapToday(globalGrowthTestAccount(t))
	if err != nil {
		t.Fatalf("GrowthHeatmapToday: %v", err)
	}
	if day.Day != "2026-10-25" || day.Score != 4 || !day.Lit {
		t.Errorf("day=%+v want 2026-10-25 score=4 lit=true", day)
	}
	if len(day.LitDays) != 2 || day.LitDays[1] != "2026-10-25" {
		t.Errorf("lit days=%v want two recent lit cells", day.LitDays)
	}
	if cnHits.Load() != 0 {
		t.Errorf("CN base hits=%d want 0", cnHits.Load())
	}
	if globalHits.Load() != 1 {
		t.Errorf("global hits=%d want 1", globalHits.Load())
	}
}

// 空热力图不能当成“今天未点亮”，否则调用方会盲发。
func TestGrowthHeatmapTodayRejectsEmptyCells(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"msg":"OK","data":{"cells":[]}}`))
	}))
	defer srv.Close()
	c := &Client{
		HTTP:              srv.Client(),
		GlobalEnabled:     true,
		BillingBaseCN:     srv.URL,
		BillingBaseGlobal: srv.URL,
	}
	if _, err := c.GrowthHeatmapToday(globalGrowthTestAccount(t)); err == nil {
		t.Fatal("empty heatmap must return an error")
	}
}

// CN 成长接口不在 billing host 上，误调只会拿到 HTML/错误信封；在客户端提前拒绝。
func TestGrowthHeatmapTodayRejectsCNAccount(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"code":0,"msg":"unexpected"}`))
	}))
	defer srv.Close()
	c := &Client{
		HTTP:              srv.Client(),
		GlobalEnabled:     true,
		BillingBaseCN:     srv.URL,
		BillingBaseGlobal: srv.URL,
	}
	a := &auth.Auth{UID: "cn-1", AccessToken: "at"}
	if _, err := auth.BackfillRealmFor(a, "cn"); err != nil {
		t.Fatalf("BackfillRealmFor(cn): %v", err)
	}
	if _, err := c.GrowthHeatmapToday(a); err == nil {
		t.Fatal("CN account must be rejected")
	}
	if hits.Load() != 0 {
		t.Errorf("upstream hits=%d want 0", hits.Load())
	}
}
