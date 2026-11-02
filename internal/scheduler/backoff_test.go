// backoff_test.go 钉住断网退避的两条口径：整轮全失败才累加、成功后清零；
// 间隔按 2^n 放大并封顶。2026-11-02 断网 6.5 小时，余额刷新与补领清扫按原频率
// 硬打上游（200+ 条失败日志），退避就是为这件事加的。
package scheduler

import (
	"testing"
	"time"
)

// init 关掉 keepalive 账号间限速：单测不该为 1.5s 的防风控间隔真睡。
func init() { keepaliveAccountDelay = 0 }

func TestBackoffInterval(t *testing.T) {
	base := 5 * time.Minute
	cases := []struct {
		fails int
		want  time.Duration
	}{
		{0, 5 * time.Minute},  // 没有失败：原间隔
		{-1, 5 * time.Minute}, // 非法值：原间隔
		{1, 10 * time.Minute},
		{2, 20 * time.Minute},
		{3, 40 * time.Minute},
		{4, 40 * time.Minute}, // 封顶
		{99, 40 * time.Minute},
	}
	for _, c := range cases {
		if got := backoffInterval(base, c.fails); got != c.want {
			t.Errorf("backoffInterval(%s, %d) = %s, want %s", base, c.fails, got, c.want)
		}
	}
	if got := backoffInterval(0, 3); got != 0 {
		t.Errorf("base=0 应原样返回 0，got %s", got)
	}
}

func TestNextFailStreak(t *testing.T) {
	cases := []struct {
		cur, failed, total, want int
	}{
		{0, 0, 3, 0},  // 全部成功 → 清零
		{2, 1, 3, 0},  // 有一个成功 → 清零
		{0, 3, 3, 1},  // 整轮全失败 → 1
		{1, 3, 3, 2},  // 连续 → 累加
		{4, 2, 2, 5},  // 封顶 5
		{3, 0, 0, 3},  // 本轮无账号参与 → 保持原值
	}
	for _, c := range cases {
		if got := nextFailStreak(c.cur, c.failed, c.total); got != c.want {
			t.Errorf("nextFailStreak(%d, %d, %d) = %d, want %d",
				c.cur, c.failed, c.total, got, c.want)
		}
	}
}
