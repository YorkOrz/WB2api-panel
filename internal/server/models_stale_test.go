// models_stale_test.go 钉住 CN 模型目录的 stale 回落：上游拉取失败时
// /v1/models 必须继续给上次成功的目录，而不是把 cn 模型全摘掉。
package server

import (
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

func countModels(t *testing.T, body []byte) int {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := resp["data"].([]any)
	return len(data)
}

func TestModelsStaleFallbackOnFetchFailure(t *testing.T) {
	dynamicModelsCache.Lock()
	dynamicModelsCache.ids = nil
	dynamicModelsCache.fetched = time.Time{}
	dynamicModelsCache.lastFail = time.Time{}
	dynamicModelsCache.Unlock()

	var fail atomic.Bool
	up := newFakeUpstream(t, func(string) (int, string, bool) {
		if fail.Load() {
			return 500, `boom`, false
		}
		return 200, `{"code":0,"data":{"models":[{"id":"dyn-a"},{"id":"dyn-b"}]}}`, false
	})
	p := testPoolWith(&auth.Auth{UID: "u1", AccessToken: "at1", ExpiresAt: 9999999999})
	h := NewHandler(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
	first := countModels(t, rec.Body.Bytes())
	if first == 0 {
		t.Fatal("首次拉取应拿到模型")
	}

	// 缓存过期 + 上游挂掉 → 应回落到上次成功的目录
	dynamicModelsCache.Lock()
	dynamicModelsCache.fetched = time.Now().Add(-dynamicModelsTTL - time.Minute)
	dynamicModelsCache.Unlock()
	fail.Store(true)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
	second := countModels(t, rec.Body.Bytes())

	if second != first {
		t.Fatalf("拉取失败后模型数 %d，want 回落到 %d（stale 好过空）", second, first)
	}
}
