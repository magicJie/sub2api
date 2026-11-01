//go:build unit

package service

import (
	"bytes"
	"testing"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/kiro"
	"github.com/stretchr/testify/require"
)

func TestKiroReportedZeroInputDoesNotBecomeEstimatedCharge(t *testing.T) {
	frame := buildKiroBridgeEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"tokenUsage": map[string]any{"uncachedInputTokens": 0, "outputTokens": 7, "totalTokens": 7},
	})
	parsed, err := kiropkg.ParseNonStreamingEventStreamWithContext(bytes.NewReader(frame), "claude-sonnet-4.5", kiropkg.KiroRequestContext{EstimatedInputTokens: 100})
	require.NoError(t, err)
	usage := kiroUsageToClaude(parsed.Usage, 100)
	require.Equal(t, 0, usage.InputTokens)
	require.Equal(t, 7, usage.OutputTokens)
	require.Equal(t, 100, kiroUsageToClaude(kiropkg.Usage{OutputTokens: 7}, 100).InputTokens)
}
