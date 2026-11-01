// streak_test.go 钉住补签的月份边界：上游补签卡只接受**当月**日期
// （400 only current month makeup allowed），每月 1 号的「昨日」必属上月，
// 必须跳过而不是发一次注定失败的请求（2026-11-01 实测 3 个账号各刷一条失败日志）。
package scheduler

import (
	"testing"
	"time"
)

func TestMakeupTargetMonthBoundary(t *testing.T) {
	loc := time.Local
	cases := []struct {
		name string
		now  time.Time
		want string
		ok   bool
	}{
		{"月初跨月 11-01→10-31", time.Date(2026, 11, 1, 9, 0, 0, 0, loc), "2026-10-31", false},
		{"跨年 01-01→12-31", time.Date(2026, 1, 1, 9, 0, 0, 0, loc), "2025-12-31", false},
		{"平年二月 03-01→02-28", time.Date(2026, 3, 1, 9, 0, 0, 0, loc), "2026-02-28", false},
		{"闰年二月 03-01→02-29", time.Date(2028, 3, 1, 9, 0, 0, 0, loc), "2028-02-29", false},
		{"同月可补 11-02→11-01", time.Date(2026, 11, 2, 9, 0, 0, 0, loc), "2026-11-01", true},
		{"月末同月 11-30→11-29", time.Date(2026, 11, 30, 9, 0, 0, 0, loc), "2026-11-29", true},
	}
	for _, c := range cases {
		got, ok := makeupTarget(c.now)
		if s := got.Format("2006-01-02"); s != c.want {
			t.Errorf("%s: date=%s want %s", c.name, s, c.want)
		}
		if ok != c.ok {
			t.Errorf("%s: ok=%v want %v", c.name, ok, c.ok)
		}
	}
}
