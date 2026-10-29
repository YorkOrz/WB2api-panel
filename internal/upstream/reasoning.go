// reasoning.go 读取请求体里的推理档位（reasoning effort）观测值。
//
// 用途是**日志 / 面板展示**，不参与任何出站改写：档位降级与补默认档仍由
// payload.go（normalizeReasoningEffort）与 thinking.go（injectThinking）负责。
// 同一个函数在两侧各调用一次：
//
//   - server 侧对客户端**原始 body** 调用 → 得到客户端请求的档位（requested）；
//   - upstream 侧对 **prepareBody 产物** 调用 → 得到真正发往上游的档位（effective）。
//
// 两者不同即说明本次请求被降级（模型不支持该档位，已按 supportedEfforts 改写），
// 面板据此显示 `high→medium`。
package upstream

import (
	"encoding/json"
	"strings"
)

// ReasoningState 一次请求的推理档位观测结果。
type ReasoningState struct {
	// Effort 推理档位（low/medium/high/xhigh/max 等，原样取自请求体）。
	// 空串 = 请求未声明档位（模型可能仍按上游默认档思考）。
	Effort string
	// Off 客户端显式关闭思考（thinking.type = disabled）。
	// 与 Effort 空串的区别：Off 是「明确要求不思考」，空串是「没说要什么」。
	Off bool
}

// ReadReasoningState 从 OpenAI 兼容请求体提取推理档位。
//
// 字段优先级与出站改写口径一致（见 payload.go normalizeReasoningEffort 的
// snake/camel 双字段兼容）：reasoning_effort > reasoningEffort > reasoning.effort。
// thinking.type=disabled 视为关闭思考（此前的档位声明按 upstream 实际行为被一并忽略）。
// body 非法 JSON 或为空时返回零值（不报错——日志路径不能因观测失败影响主流程）。
func ReadReasoningState(body []byte) ReasoningState {
	if len(body) == 0 {
		return ReasoningState{}
	}
	var obj struct {
		Snake string `json:"reasoning_effort"`
		Camel string `json:"reasoningEffort"`
		Inner struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		Thinking struct {
			Type string `json:"type"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		return ReasoningState{}
	}
	// 显式关闭思考优先：上游在 disabled 分支会删掉 reasoning_effort
	// （thinking.go），此时报档位会与真实出站行为矛盾。
	if isThinkingDisabled(obj.Thinking.Type) {
		return ReasoningState{Off: true}
	}
	for _, e := range []string{obj.Snake, obj.Camel, obj.Inner.Effort} {
		if e == "" {
			continue
		}
		// effort 字面量 "none"/"off" 是「不思考」的另一种写法：OpenAI Responses
		// 方言里 reasoning.effort="none" 即关闭推理，DeepSeek Harness 这类客户端
		// 未选档位时会直接把它发出来（见其 thinkingLevelMap.off 兜底 "none"）。
		// 归一到 Off，避免面板显示成「档位 none」这种并不存在的档位。
		if isThinkingDisabled(e) {
			return ReasoningState{Off: true}
		}
		return ReasoningState{Effort: e}
	}
	return ReasoningState{}
}

// isThinkingDisabled 判定 thinking.type 是否为关闭态（大小写与空白容忍）。
func isThinkingDisabled(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "disabled", "disable", "off", "none", "false":
		return true
	}
	return false
}
