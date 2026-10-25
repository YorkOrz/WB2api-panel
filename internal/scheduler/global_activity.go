// global_activity.go 国际版每日活跃补跑。
//
// Global 号没有 CN 签到体系，但成长中心热力图会在当天 score>0 时于次日
// 发放 Bonus Pack。点亮条件是官方聊天事件 chat_request_send，即现有
// /v2/report 接口。这里以热力图作为唯一判据：
//
//   - score > 0：already-lit，不发任何上报；
//   - score == 0：只发一次 report，失败留给下个时点，不在同轮重试；
//   - 热力图读取失败：failed，绝不盲发。
//
// 多个时点只是提高网络/唤醒失败后的补跑概率；进程内再按 API 返回的自然日
// 记住已成功上报的账号，避免手动与定时并发时因热力图聚合延迟而重复上报。
//
// 行为口径参考 MIT 项目 codebuddy-intl-cpa 的 workbuddy/dailybonus.go。
package scheduler

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/logfmt"
	"github.com/linguo2625469/workbuddy2api-panel/internal/session"
)

const (
	globalActivityAlreadyLit = "already-lit"
	globalActivityReported   = "reported"
	globalActivityFailed     = "failed"
	globalActivitySkipped    = "skipped"
)

// globalActivityJitterMax 定时入口的随机错峰上限。手动补跑不等待；定时入口先
// 错峰再读热力图，避免所有部署在整点形成固定请求节奏。测试可置 0。
var globalActivityJitterMax = 4 * time.Minute

// GlobalActivityResult 是单个 Global 账号最近一次每日活跃检查的结果。
// Lit 只表示热力图回读为已点亮；Status=reported 时 Lit 可能仍为 false，
// 因为上游热力图聚合存在数秒到数分钟延迟。
type GlobalActivityResult struct {
	UID      string    `json:"uid"`
	Nickname string    `json:"nickname,omitempty"`
	Day      string    `json:"day,omitempty"`
	Score    int       `json:"score"`
	Lit      bool      `json:"lit"`
	LitDays  []string  `json:"lit_days,omitempty"`
	Status   string    `json:"status"`
	Detail   string    `json:"detail,omitempty"`
	RunAt    time.Time `json:"run_at"`
}

// RunGlobalActivityNow 立即检查全部 Global 账号并返回逐号结果。
func (s *Scheduler) RunGlobalActivityNow() []GlobalActivityResult {
	return s.runGlobalActivity(context.Background())
}

// RunGlobalActivityForUID 手工补跑单个 Global 账号。手工入口允许禁用账号，
// 便于运维在停止选号后仍补当天活跃。
func (s *Scheduler) RunGlobalActivityForUID(uid string) (GlobalActivityResult, error) {
	if s == nil || s.cfg.Pool == nil || s.cfg.Upstream == nil {
		return GlobalActivityResult{}, fmt.Errorf("scheduler not available")
	}
	a := s.cfg.Pool.AuthByUID(uid)
	if a == nil {
		return GlobalActivityResult{}, fmt.Errorf("account not found")
	}
	if !a.IsGlobal() {
		return GlobalActivityResult{}, fmt.Errorf("not a Global account")
	}

	s.globalRunMu.Lock()
	defer s.globalRunMu.Unlock()
	row := s.runGlobalActivityOneLocked(a, uid, a.Nickname)
	s.rememberGlobalActivityLocked(row)
	s.logGlobalActivityLocked(row)
	return row, nil
}

// GlobalActivityStatus 返回当前进程内最近一次逐号状态（按账号池顺序）。
func (s *Scheduler) GlobalActivityStatus() []GlobalActivityResult {
	if s == nil || s.cfg.Pool == nil {
		return nil
	}
	s.globalStateMu.RLock()
	defer s.globalStateMu.RUnlock()
	out := make([]GlobalActivityResult, 0, len(s.globalActivityLast))
	for _, st := range s.cfg.Pool.List() {
		if row, ok := s.globalActivityLast[st.UID]; ok {
			out = append(out, row)
		}
	}
	return out
}

func (s *Scheduler) runGlobalActivity(ctx context.Context) []GlobalActivityResult {
	if s == nil || s.cfg.Pool == nil || s.cfg.Upstream == nil {
		return nil
	}
	s.globalRunMu.Lock()
	defer s.globalRunMu.Unlock()

	includeDisabled := s.includeDisabledInTasks()
	results := make([]GlobalActivityResult, 0)
	first := true
	for _, st := range s.cfg.Pool.List() {
		if ctx.Err() != nil {
			break
		}
		if st.Disabled && !includeDisabled {
			continue
		}
		a := s.cfg.Pool.AuthByUID(st.UID)
		if a == nil || !a.IsGlobal() {
			continue
		}
		if !first {
			if !sleepCtx(ctx, activityAccountDelay) {
				break
			}
		}
		first = false
		row := s.runGlobalActivityOneLocked(a, st.UID, st.Nickname)
		s.rememberGlobalActivityLocked(row)
		s.logGlobalActivityLocked(row)
		results = append(results, row)
	}
	return results
}

func (s *Scheduler) runScheduledGlobalActivity(ctx context.Context) []GlobalActivityResult {
	if globalActivityJitterMax > 0 {
		delay := time.Duration(rand.Int64N(int64(globalActivityJitterMax)))
		if delay > 0 {
			log.Printf("global-activity schedule jitter %s", delay.Round(time.Second))
			if !sleepCtx(ctx, delay) {
				return nil
			}
		}
	}
	return s.runGlobalActivity(ctx)
}

func (s *Scheduler) runGlobalActivityOneLocked(a *auth.Auth, uid, nickname string) GlobalActivityResult {
	row := GlobalActivityResult{UID: uid, Nickname: nickname, RunAt: time.Now()}
	if a == nil || a.AccessTokenValue() == "" {
		row.Status, row.Detail = globalActivitySkipped, "no access token"
		return row
	}
	if !a.IsGlobal() {
		row.Status, row.Detail = globalActivitySkipped, "not a Global account"
		return row
	}
	if a.IsEnterpriseSession() {
		row.Status, row.Detail = globalActivitySkipped, "enterprise session has no personal growth"
		return row
	}

	day, err := s.cfg.Upstream.GrowthHeatmapToday(a)
	if err != nil {
		row.Status, row.Detail = globalActivityFailed, "heatmap: "+shortGlobalActivityDetail(err.Error())
		return row
	}
	row.Day, row.Score, row.Lit, row.LitDays = day.Day, day.Score, day.Lit, day.LitDays
	if day.Lit {
		s.globalStateMu.Lock()
		delete(s.globalActivityReported, uid)
		s.globalStateMu.Unlock()
		row.Status = globalActivityAlreadyLit
		return row
	}
	s.globalStateMu.RLock()
	if reportedDay := s.globalActivityReported[uid]; reportedDay == day.Day {
		s.globalStateMu.RUnlock()
		row.Status = globalActivityReported
		row.Detail = "report already sent today; waiting for heatmap aggregation"
		return row
	}
	s.globalStateMu.RUnlock()

	// 官方会话/请求 ID 是 32 位随机 hex；不要使用带项目前缀的时间戳，
	// 那会形成明显的客户端指纹。
	conversationID := session.NewMessageID()
	if err := s.cfg.Upstream.ReportChatActivity(a, conversationID, ""); err != nil {
		row.Status, row.Detail = globalActivityFailed, "report: "+shortGlobalActivityDetail(err.Error())
		return row
	}
	s.globalStateMu.Lock()
	s.globalActivityReported[uid] = day.Day
	s.globalStateMu.Unlock()
	row.Status = globalActivityReported
	return row
}

func (s *Scheduler) rememberGlobalActivityLocked(row GlobalActivityResult) {
	s.globalStateMu.Lock()
	defer s.globalStateMu.Unlock()
	if s.globalActivityLast == nil {
		s.globalActivityLast = make(map[string]GlobalActivityResult)
	}
	s.globalActivityLast[row.UID] = row
}

func (s *Scheduler) logGlobalActivityLocked(row GlobalActivityResult) {
	detail := ""
	if row.Detail != "" {
		detail = " detail=" + row.Detail
	}
	log.Printf("global-activity %s: day=%s score=%d lit=%t status=%s%s",
		logfmt.Label(row.UID, row.Nickname), row.Day, row.Score, row.Lit, row.Status, detail)
}

func shortGlobalActivityDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 220 {
		return s[:220] + "..."
	}
	return s
}
