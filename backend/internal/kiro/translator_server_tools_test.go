package kiro

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMixedNativeSearchToolsDoNotBecomeClientCalls(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tools    string
		choice   string
		rejected bool
	}{
		{"native", `[{"name":"Read","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}]`, "", true},
		{"native_without_name", `[{"name":"Read","input_schema":{"type":"object"}},{"type":"web_search_20250305"}]`, "", true},
		{"google_server_search", `[{"name":"Read","input_schema":{"type":"object"}},{"type":"google_search"}]`, "", true},
		{"custom_lowercase", `[{"name":"Read","input_schema":{"type":"object"}},{"name":"web_search","input_schema":{"type":"object"}}]`, "", false},
		{"custom_titlecase", `[{"name":"Read","input_schema":{"type":"object"}},{"name":"WebSearch","input_schema":{"type":"object"}}]`, "", false},
		{"single_native", `[{"type":"web_search_20250305","name":"web_search"}]`, "", false},
		{"disabled_native", `[{"name":"Read","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}]`, `,"tool_choice":{"type":"none"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"tools":%s,"messages":[{"role":"user","content":"find Go release notes"}]%s}`, tc.tools, tc.choice))
			_, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
			if tc.rejected {
				var invalid *InvalidRequestError
				require.ErrorAs(t, err, &invalid)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
