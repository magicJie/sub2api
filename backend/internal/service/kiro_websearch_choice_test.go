//go:build unit

package service

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKiroWebSearchDisabledChoiceStopsBeforeUpstream(t *testing.T) {
	for _, choice := range []string{`"none"`, `{"type":"none"}`} {
		t.Run(choice, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4.5","tool_choice":%s,"tools":[{"name":"web_search","type":"web_search_20250305"}],"messages":[{"role":"user","content":"search for Go"}]}`, choice))
			require.False(t, isOnlyWebSearchToolInBody(body))
			// No account or HTTP client is installed: a forbidden execution would reach the upstream boundary.
			svc := &GatewayService{}
			_, err := svc.executeKiroWebSearch(t.Context(), nil, nil, body, "claude-sonnet-4.5", "claude-sonnet-4.5", "", nil)
			require.ErrorIs(t, err, errKiroWebSearchFallback)
			err = svc.streamKiroWebSearchAsAnthropic(t.Context(), nil, body, "claude-sonnet-4.5", "claude-sonnet-4.5", "", 0, nil, &bytes.Buffer{}, nil)
			require.ErrorIs(t, err, errKiroWebSearchFallback)
		})
	}
	require.True(t, isOnlyWebSearchToolInBody([]byte(`{"tools":[{"name":"web_search"}],"tool_choice":{"type":"auto"}}`)))
}
