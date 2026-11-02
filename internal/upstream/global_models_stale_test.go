// global_models_stale_test.go 钉住 global 模型目录的 stale 回落：
// 断网时 /v1/models 不能把 global 模型整段摘掉（2026-11-02 实测 6.5 小时断网，
// 旧实现把 names/infos 直接置 nil，客户端看到模型"消失"）。
package upstream

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

func globalTestAuth() *auth.Auth {
	return &auth.Auth{UID: "g1", AccessToken: "at", Domain: "www.workbuddy.ai"}
}

// TestGlobalModelsNegativeCacheReturnsStale 负缓存冷却期内返回上次成功的目录。
func TestGlobalModelsNegativeCacheReturnsStale(t *testing.T) {
	c := &Client{GlobalEnabled: true}
	c.globalModels.names = []string{"gpt-5.5", "glm-5.3"}
	c.globalModels.infos = []ModelInfo{{ID: "gpt-5.5"}, {ID: "glm-5.3"}}
	c.globalModels.fetched = time.Now().Add(-2 * time.Hour) // 已过 TTL
	c.globalModels.lastFail = time.Now()                    // 负缓存生效

	names, infos := c.fetchGlobalModelsOnce(globalTestAuth())
	if len(names) != 2 || names[0] != "gpt-5.5" {
		t.Fatalf("names=%v want stale 两条", names)
	}
	if len(infos) != 2 {
		t.Errorf("infos=%d want 2（对象形态条目同样要回落）", len(infos))
	}
}

// TestGlobalModelsProbeFailureKeepsStale 探测真失败时保留缓存，不清空。
func TestGlobalModelsProbeFailureKeepsStale(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Client{
		GlobalEnabled:     true,
		HTTP:              srv.Client(),
		ChatBaseGlobal:    srv.URL,
		BillingBaseGlobal: srv.URL,
	}
	c.globalModels.names = []string{"gpt-5.5"}
	c.globalModels.infos = []ModelInfo{{ID: "gpt-5.5"}}
	c.globalModels.fetched = time.Now().Add(-2 * time.Hour) // 过期 → 会真探测

	names, _ := c.fetchGlobalModelsOnce(globalTestAuth())
	if len(names) != 1 || names[0] != "gpt-5.5" {
		t.Fatalf("names=%v want stale [gpt-5.5]（探测失败不得清空目录）", names)
	}
	c.globalModels.Lock()
	defer c.globalModels.Unlock()
	if len(c.globalModels.names) != 1 {
		t.Errorf("缓存被清空: %v", c.globalModels.names)
	}
	if c.globalModels.lastFail.IsZero() {
		t.Error("失败应写负缓存，避免下一请求立刻再探测")
	}
}
