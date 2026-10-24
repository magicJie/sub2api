package kiro

// Prompt cache 断点（cachePoint）。
//
// Kiro 的 generateAssistantResponse 接受 Bedrock 风格的 cachePoint（{"type":"default"}），
// 可放在 userInputMessage、assistantResponseMessage 上，或作为 tools 列表中紧随某个工具的一项；
// 语义与 Anthropic 的 cache_control 相同：缓存到断点为止的前缀，单个请求最多 4 个。
// 这里把客户端（如 Claude Code）的 cache_control 原样转成 cachePoint，不改变任何内容。
// 参考 magpie-community/plugins packages/kiro（MIT）。

import (
	"os"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// newKiroCachePoint 返回一个断点；SUB2API_KIRO_CACHE_POINTS=off 时返回 nil（不发送断点）。
func newKiroCachePoint() *KiroCachePoint {
	if kiroCachePointsDisabled() {
		return nil
	}
	return &KiroCachePoint{Type: "default"}
}

func kiroCachePointsDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_CACHE_POINTS"))) {
	case "0", "false", "off", "no":
		return true
	default:
		return false
	}
}

// hasKiroCacheControl 报告消息本身或其任一内容块是否带 cache_control。
func hasKiroCacheControl(msg gjson.Result) bool {
	if msg.Get("cache_control").Exists() {
		return true
	}
	content := msg.Get("content")
	if !content.IsArray() {
		return false
	}
	for _, part := range content.Array() {
		if part.Get("cache_control").Exists() {
			return true
		}
	}
	return false
}

// systemHasKiroCacheControl 报告顶层 system 是否带 cache_control（system 位于第一条 user 历史消息），
// 以及开头（没有前一条消息）的 system 消息是否带 cache_control。中间的 system 消息的断点
// 由 extractInlineSystemPrompts 留在原位置。
func systemHasKiroCacheControl(claudeBody []byte, messages gjson.Result) bool {
	system := gjson.GetBytes(claudeBody, "system")
	if system.IsArray() {
		for _, block := range system.Array() {
			if block.Get("cache_control").Exists() {
				return true
			}
		}
	}
	for _, message := range messages.Array() {
		if message.Get("role").String() != "system" {
			return false
		}
		if hasKiroCacheControl(message) {
			return true
		}
	}
	return false
}

// withKiroMessageCacheControl 在消息级别加上 cache_control 标记（仅供本包判断断点位置）。
func withKiroMessageCacheControl(msg gjson.Result) gjson.Result {
	if msg.Get("cache_control").Exists() {
		return msg
	}
	updated, err := sjson.Set(msg.Raw, "cache_control", map[string]string{"type": "ephemeral"})
	if err != nil {
		return msg
	}
	return gjson.Parse(updated)
}

// limitKiroCachePoints 把请求中的 cachePoint 限制在 max 个以内，保留靠后的那些
// （覆盖的前缀最长）。计数顺序：current 消息里的工具断点、历史消息、current 消息本身，
// 与 tools 在请求中先于对话被缓存的顺序一致。
func limitKiroCachePoints(history []KiroHistoryMessage, current *KiroUserInputMessage, max int) {
	var drops []func()
	if current != nil && current.UserInputMessageContext != nil {
		tools := current.UserInputMessageContext.Tools
		for i := range tools {
			if tools[i].CachePoint != nil {
				idx := i
				drops = append(drops, func() { tools[idx].CachePoint = nil })
			}
		}
	}
	for i := range history {
		if u := history[i].UserInputMessage; u != nil && u.CachePoint != nil {
			drops = append(drops, func() { u.CachePoint = nil })
		}
		if a := history[i].AssistantResponseMessage; a != nil && a.CachePoint != nil {
			drops = append(drops, func() { a.CachePoint = nil })
		}
	}
	if current != nil && current.CachePoint != nil {
		drops = append(drops, func() { current.CachePoint = nil })
	}
	for i := 0; i < len(drops)-max; i++ {
		drops[i]()
	}
	if current != nil && current.UserInputMessageContext != nil {
		current.UserInputMessageContext.Tools = compactKiroCachePointTools(current.UserInputMessageContext.Tools)
	}
}

// compactKiroCachePointTools 去掉被清空的 cachePoint 占位项。
func compactKiroCachePointTools(tools []KiroToolWrapper) []KiroToolWrapper {
	out := tools[:0]
	for _, tool := range tools {
		if tool.ToolSpecification == nil && tool.CachePoint == nil {
			continue
		}
		out = append(out, tool)
	}
	return out
}
