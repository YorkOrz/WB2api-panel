package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/scheduler"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

// 面板单号补跑入口必须复用调度器热力图门禁，并把结果回显到 overview。
func TestAccountGlobalActivityEndpointAndOverviewStatus(t *testing.T) {
	var reportHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/activity/growth/heatmap":
			w.Write([]byte(`{"code":0,"data":{"cells":[{"date":"2026-10-25","score":0}]}}`))
		case "/v2/report":
			var events []struct {
				ConversationID string `json:"conversationId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
				t.Errorf("decode report: %v", err)
			} else if len(events) != 1 {
				t.Errorf("report events=%d want 1", len(events))
			} else if id := events[0].ConversationID; len(id) != 32 || strings.Contains(id, "wb2api") {
				t.Errorf("conversationId=%q want official-looking 32 hex ID", id)
			}
			reportHits.Add(1)
			w.Write([]byte(`{"code":0,"msg":"OK"}`))
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	a := &auth.Auth{UID: "g1", AccessToken: "at", Nickname: "global-one"}
	if _, err := auth.BackfillRealmFor(a, "global"); err != nil {
		t.Fatal(err)
	}
	p := pool.New("")
	p.Add(a)
	up := &upstream.Client{
		HTTP:              srv.Client(),
		GlobalEnabled:     true,
		BillingBaseCN:     srv.URL,
		BillingBaseGlobal: srv.URL,
	}
	sch := scheduler.New(scheduler.Config{Pool: p, Upstream: up})
	panel := New(Config{Pool: p, Upstream: up, Scheduler: sch})

	rec := httptest.NewRecorder()
	panel.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/panel/api/accounts/g1/global_activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK     bool                           `json:"ok"`
		Result scheduler.GlobalActivityResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK || resp.Result.Status != "reported" || resp.Result.Day != "2026-10-25" {
		t.Errorf("response=%+v want reported on 2026-10-25", resp)
	}
	if reportHits.Load() != 1 {
		t.Errorf("report hits=%d want 1", reportHits.Load())
	}

	rec = httptest.NewRecorder()
	panel.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel/api/overview", nil))
	var overview struct {
		GlobalActivity []scheduler.GlobalActivityResult `json:"global_activity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if len(overview.GlobalActivity) != 1 || overview.GlobalActivity[0].UID != "g1" {
		t.Errorf("overview global_activity=%+v want g1", overview.GlobalActivity)
	}
}

// TestGlobalTaskListUsesMilestoneSchemaAndIsReadOnly 国际号任务使用
// code/url/status 字段；面板必须正确展示，并拒绝没有验证过的 accept/claim 调用。
func TestGlobalTaskListUsesMilestoneSchemaAndIsReadOnly(t *testing.T) {
	var listHits, mutationHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/activity/growth/tasks" && r.Method == http.MethodGet:
			listHits.Add(1)
			w.Write([]byte(`{"code":0,"data":{"tasks":[
				{"task_id":1,"code":"first_chat","title":"完成一次对话","status":"available","url":"workbuddy://task/first_chat"},
				{"task_id":3,"code":"wechat_linked","title":"链接微信","status":"available"}
			]}}`))
		case strings.Contains(r.URL.Path, "/activity/growth/tasks/"):
			mutationHits.Add(1)
			http.Error(w, "unexpected mutation", http.StatusInternalServerError)
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	a := &auth.Auth{UID: "global-tasks", AccessToken: "at", Nickname: "global"}
	if _, err := auth.BackfillRealmFor(a, "global"); err != nil {
		t.Fatal(err)
	}
	p := pool.New("")
	p.Add(a)
	up := &upstream.Client{
		HTTP:              srv.Client(),
		GlobalEnabled:     true,
		ChatBaseGlobal:    srv.URL,
		BillingBaseGlobal: srv.URL,
	}
	panel := New(Config{Pool: p, Upstream: up})

	rec := httptest.NewRecorder()
	panel.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel/api/accounts/global-tasks/tasks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Realm string          `json:"realm"`
		Tasks []upstream.Task `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if resp.Realm != "global" || len(resp.Tasks) != 2 {
		t.Fatalf("response=%+v want 2 Global tasks", resp)
	}
	if got := resp.Tasks[0]; got.TaskCode != "first_chat" || got.JumpURL == "" || got.Status != "available" {
		t.Errorf("first task=%+v want Global milestone fields", got)
	}
	if listHits.Load() != 1 {
		t.Errorf("list hits=%d want 1（Global 不应再打 CN 小程序列表）", listHits.Load())
	}

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"accept_all", "/panel/api/accounts/global-tasks/tasks/accept_all", ""},
		{"claim", "/panel/api/accounts/global-tasks/tasks/claim", `{"task_code":"first_chat"}`},
	} {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		panel.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s status=%d want 409 body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}
	if mutationHits.Load() != 0 {
		t.Errorf("Global mutation hits=%d want 0", mutationHits.Load())
	}
}
