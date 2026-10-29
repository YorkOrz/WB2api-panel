package upstream

import "testing"

// ReadReasoningState 必须覆盖出站改写实际认的全部字段形态（snake / camel /
// reasoning.effort）以及「显式关闭思考」，否则面板会把档位显示成与真实出站矛盾的值。
func TestReadReasoningState(t *testing.T) {
	cases := []struct {
		name string
		body string
		want ReasoningState
	}{
		{"snake", `{"model":"m","reasoning_effort":"high"}`, ReasoningState{Effort: "high"}},
		{"camel", `{"model":"m","reasoningEffort":"max"}`, ReasoningState{Effort: "max"}},
		{"nested", `{"model":"m","reasoning":{"effort":"low"}}`, ReasoningState{Effort: "low"}},
		// snake 优先于 camel 与嵌套（与 payload.go 双字段兼容口径一致）。
		{"snake wins", `{"reasoning_effort":"high","reasoningEffort":"low","reasoning":{"effort":"max"}}`, ReasoningState{Effort: "high"}},
		{"absent", `{"model":"m","messages":[]}`, ReasoningState{}},
		{"empty effort", `{"reasoning_effort":""}`, ReasoningState{}},
		{"thinking disabled", `{"reasoning_effort":"high","thinking":{"type":"disabled"}}`, ReasoningState{Off: true}},
		{"thinking disabled upper", `{"thinking":{"type":"DISABLED"}}`, ReasoningState{Off: true}},
		{"thinking disabled spaced", `{"thinking":{"type":" off "}}`, ReasoningState{Off: true}},
		// enabled 不改变档位（开启思考 ≠ 指定档位）。
		{"thinking enabled keeps effort", `{"reasoning_effort":"low","thinking":{"type":"enabled"}}`, ReasoningState{Effort: "low"}},
		{"thinking enabled only", `{"thinking":{"type":"enabled"}}`, ReasoningState{}},
		// effort 字面量 none/off 等价于关闭思考（Responses 方言 + DSH 的 off 兜底）。
		{"effort none", `{"reasoning_effort":"none"}`, ReasoningState{Off: true}},
		{"effort off", `{"reasoning_effort":"off"}`, ReasoningState{Off: true}},
		{"nested effort none", `{"reasoning":{"effort":"none"}}`, ReasoningState{Off: true}},
		{"nil body", ``, ReasoningState{}},
		{"invalid json", `{not json`, ReasoningState{}},
	}
	for _, c := range cases {
		if got := ReadReasoningState([]byte(c.body)); got != c.want {
			t.Errorf("%s: ReadReasoningState(%s)=%+v want %+v", c.name, c.body, got, c.want)
		}
	}
}

// 出站一致性：prepareBody 补默认档后，ReadReasoningState 读到的就是真正发往上游的档位。
// 这条断言守住「回填值取自 prepareBody 产物」这个前提——一旦有人在回填前取原始
// body，deepseek 这类缺档补默认的模型会显示成无档位。
func TestReadReasoningStateAfterPrepareInjectsDefault(t *testing.T) {
	// deepseek 系缺档时 thinking.go 补默认档（无声明默认档 → 硬编码 high）。
	prepared := PrepareBodyOptWithEffortsAndDefault(
		[]byte(`{"model":"deepseek-v4-flash","messages":[]}`), false, nil, nil)
	if got := ReadReasoningState(prepared); got.Effort != "high" {
		t.Fatalf("prepared body effort=%q want high (body=%s)", got.Effort, prepared)
	}
}

// 降级可见性：请求 max、模型只支持到高 → 出站是 supportedEfforts 里的档位，
// ReadReasoningState 报的是改写后的值（与客户端请求值不同，面板据此显示 A→B）。
func TestReadReasoningStateAfterDowngrade(t *testing.T) {
	efforts := map[string][]string{"glm-local": {"low", "high"}}
	prepared := PrepareBodyOptWithEffortsAndDefault(
		[]byte(`{"model":"glm-local","reasoning_effort":"max","messages":[]}`), false, efforts, nil)
	got := ReadReasoningState(prepared)
	if got.Effort == "max" {
		t.Fatalf("effort must be downgraded, got %q (body=%s)", got.Effort, prepared)
	}
	if got.Effort != "high" {
		t.Fatalf("downgraded effort=%q want high (body=%s)", got.Effort, prepared)
	}
}
