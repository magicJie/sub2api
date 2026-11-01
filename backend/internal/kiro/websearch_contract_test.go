package kiro

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSearchResultsReferToTheirServerToolUse(t *testing.T) {
	searches := []SearchIndicator{
		{ToolUseID: "srvtoolu_first", Query: "first"},
		{ToolUseID: "srvtoolu_second", Query: "second"},
	}
	for _, format := range []string{"buffered", "streaming"} {
		t.Run(format, func(t *testing.T) {
			var blocks []gjson.Result
			if format == "buffered" {
				body, err := InjectSearchIndicatorsInResponse([]byte(`{"content":[{"type":"text","text":"answer"}]}`), searches)
				require.NoError(t, err)
				blocks = gjson.GetBytes(body, "content").Array()
			} else {
				for i, search := range searches {
					for _, chunk := range GenerateSearchIndicatorEvents(search.Query, search.ToolUseID, search.Results, 2*i) {
						for _, line := range strings.Split(string(chunk), "\n") {
							if payload, ok := strings.CutPrefix(line, "data: "); ok {
								block := gjson.Get(payload, "content_block")
								if block.Exists() {
									blocks = append(blocks, block)
								}
							}
						}
					}
				}
			}
			uses := make(map[string]bool)
			matched := make(map[string]int)
			for _, block := range blocks {
				switch block.Get("type").String() {
				case "server_tool_use":
					uses[block.Get("id").String()] = true
				case "web_search_tool_result":
					id := block.Get("tool_use_id").String()
					require.True(t, uses[id], "result must refer to an earlier server_tool_use: %q", id)
					matched[id]++
				}
			}
			require.Equal(t, map[string]int{"srvtoolu_first": 1, "srvtoolu_second": 1}, matched)
		})
	}
}
