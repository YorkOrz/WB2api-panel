// growth_daily.go Global 每日活跃状态：GET /activity/growth/heatmap。
//
// 热力图最后一天是上游 API 自己的“今天”，因此不需要在本地做时区换算。
// score > 0 表示当天已点亮；score == 0 才允许补一条 chat_request_send。
package upstream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
)

const growthHeatmapPath = "/activity/growth/heatmap"

// GrowthHeatmapCell 是热力图中的一个自然日。
type GrowthHeatmapCell struct {
	Date  string `json:"date"`
	Score int    `json:"score"`
}

// GrowthDayStatus 是面板与调度器共用的当日活跃快照。
type GrowthDayStatus struct {
	Day     string    `json:"day"`
	Score   int       `json:"score"`
	Lit     bool      `json:"lit"`
	LitDays []string  `json:"lit_days,omitempty"`
	ReadAt  time.Time `json:"read_at"`
}

// GrowthHeatmapToday 读取账号所在域的热力图，以最后一个 cell 为“今天”。
// 该端点属于 billing 域：Global 必须走 BillingBaseGlobal，CN 不能误打。
func (c *Client) GrowthHeatmapToday(a *auth.Auth) (*GrowthDayStatus, error) {
	if a == nil {
		return nil, fmt.Errorf("growth heatmap: nil account")
	}
	if !a.IsGlobal() {
		return nil, fmt.Errorf("growth heatmap: not a Global account")
	}
	data, err := c.billingJSON(a, http.MethodGet, growthHeatmapPath, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Cells []GrowthHeatmapCell `json:"cells"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("growth heatmap decode: %w", err)
	}
	if len(resp.Cells) == 0 {
		return nil, fmt.Errorf("growth heatmap empty")
	}
	today := resp.Cells[len(resp.Cells)-1]
	if today.Date == "" {
		return nil, fmt.Errorf("growth heatmap last cell has no date")
	}
	litDays := make([]string, 0, len(resp.Cells))
	for _, cell := range resp.Cells {
		if cell.Score > 0 {
			litDays = append(litDays, cell.Date)
		}
	}
	if len(litDays) > 30 {
		litDays = litDays[len(litDays)-30:]
	}
	return &GrowthDayStatus{
		Day:     today.Date,
		Score:   today.Score,
		Lit:     today.Score > 0,
		LitDays: litDays,
		ReadAt:  time.Now(),
	}, nil
}
