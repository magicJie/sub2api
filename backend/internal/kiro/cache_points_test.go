package kiro

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func buildCachePointPayload(t *testing.T, body string) gjson.Result {
	t.Helper()
	result, err := BuildKiroPayloadWithContext([]byte(body), MapModel("claude-sonnet-4-5"), "", "AI_EDITOR", nil)
	require.NoError(t, err)
	return gjson.ParseBytes(result.Payload).Get("conversationState")
}

func countKiroCachePoints(state gjson.Result) int {
	n := 0
	for _, h := range state.Get("history").Array() {
		if h.Get("userInputMessage.cachePoint").Exists() || h.Get("assistantResponseMessage.cachePoint").Exists() {
			n++
		}
	}
	if state.Get("currentMessage.userInputMessage.cachePoint").Exists() {
		n++
	}
	for _, tool := range state.Get("currentMessage.userInputMessage.userInputMessageContext.tools").Array() {
		if tool.Get("cachePoint").Exists() {
			n++
		}
	}
	return n
}

// Claude Code 的标记位置（取自 magpie 插件里抓到的真实请求，内容已截短）：
// system 末尾两块、以及最后一条消息（工具结果之后 hook 注入的 system 消息）。
func TestKiroCachePointsFollowClaudeCodeMarks(t *testing.T) {
	state := buildCachePointPayload(t, `{
		"model":"claude-sonnet-4-5","max_tokens":64,
		"system":[
			{"type":"text","text":"x-anthropic-billing-header: cc_version=2"},
			{"type":"text","text":"You are a Claude agent.","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"You are an agent working with the user.","cache_control":{"type":"ephemeral"}}
		],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"run echo hi"}]},
			{"role":"system","content":"# Environment"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"echo hi"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"hi"}]},
			{"role":"system","content":[{"type":"text","text":"PreToolUse hook context","cache_control":{"type":"ephemeral"}}]}
		],
		"tools":[{"name":"Bash","description":"Run a command","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}]
	}`)

	history := state.Get("history").Array()
	require.Equal(t, "default", history[0].Get("userInputMessage.cachePoint.type").String(), "system 所在的第一条消息")
	require.False(t, history[1].Get("assistantResponseMessage.cachePoint").Exists(), "身份确认消息不带断点")
	require.Equal(t, "default", state.Get("currentMessage.userInputMessage.cachePoint.type").String(), "hook 的 system 消息并入工具结果所在的 current 消息")
	tools := state.Get("currentMessage.userInputMessage.userInputMessageContext.tools").Array()
	require.Len(t, tools, 1, "未标记的工具后面没有 cachePoint")
	require.Equal(t, "Bash", tools[0].Get("toolSpecification.name").String())
	require.False(t, tools[0].Get("cachePoint").Exists())
}

func TestKiroCachePointsMarkedToolAndLimit(t *testing.T) {
	state := buildCachePointPayload(t, `{
		"model":"claude-sonnet-4-5","max_tokens":64,
		"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}]},
			{"role":"assistant","content":[{"type":"text","text":"b","cache_control":{"type":"ephemeral"}}]},
			{"role":"user","content":[{"type":"text","text":"c","cache_control":{"type":"ephemeral"}}]}
		],
		"tools":[{"name":"Read","description":"Read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]
	}`)

	// 共 5 个标记（工具、system、a、b、c），只保留最后 4 个：最先的工具断点被丢弃。
	require.Equal(t, kiroMaxCachePoints, countKiroCachePoints(state))
	tools := state.Get("currentMessage.userInputMessage.userInputMessageContext.tools").Array()
	require.Len(t, tools, 1)
	require.Equal(t, "Read", tools[0].Get("toolSpecification.name").String())
	require.Equal(t, "default", state.Get("currentMessage.userInputMessage.cachePoint.type").String())
}

func TestKiroCachePointsMarkedToolKeepsFollowingPoint(t *testing.T) {
	state := buildCachePointPayload(t, `{
		"model":"claude-sonnet-4-5","max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"tools":[
			{"name":"Read","description":"Read","input_schema":{"type":"object"}},
			{"name":"Write","description":"Write","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}
		]
	}`)
	tools := state.Get("currentMessage.userInputMessage.userInputMessageContext.tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "Write", tools[1].Get("toolSpecification.name").String())
	require.Equal(t, "default", tools[2].Get("cachePoint.type").String())
	require.False(t, tools[2].Get("toolSpecification").Exists())
}

func TestKiroCachePointsAbsentWithoutMarks(t *testing.T) {
	state := buildCachePointPayload(t, `{"model":"claude-sonnet-4-5","max_tokens":64,"system":"s","messages":[{"role":"user","content":"hi"}]}`)
	require.Zero(t, countKiroCachePoints(state))
}

func TestKiroCachePointsCanBeDisabled(t *testing.T) {
	t.Setenv("SUB2API_KIRO_CACHE_POINTS", "off")
	state := buildCachePointPayload(t, `{
		"model":"claude-sonnet-4-5","max_tokens":64,
		"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}]}],
		"tools":[{"name":"Read","description":"Read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]
	}`)
	require.Zero(t, countKiroCachePoints(state))
	require.Len(t, state.Get("currentMessage.userInputMessage.userInputMessageContext.tools").Array(), 1)
}
