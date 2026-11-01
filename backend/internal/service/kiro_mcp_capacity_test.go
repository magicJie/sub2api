//go:build unit

package service

import (
	"net/http"
	"testing"

	kirocooldown "github.com/Wei-Shaw/sub2api/internal/kiro/cooldown"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestKiroMCPCapacityPreservesAccountEligibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		limited bool
	}{
		{"capacity", `{"reason":"INSUFFICIENT_MODEL_CAPACITY"}`, false},
		{"rate_limit", `{"message":"Too many requests"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			store := kirocooldown.NewStore(client)
			account := kiroBridgeTestAccount()
			svc := &GatewayService{
				httpUpstream:      &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusTooManyRequests, tc.body)}},
				kiroCooldownStore: store,
			}
			response, _, err := svc.doKiroMCPJSONRequest(t.Context(), account, "https://q.us-east-1.amazonaws.com/mcp", []byte(`{"jsonrpc":"2.0","id":"probe","method":"tools/list"}`), "synthetic-token")
			require.NoError(t, err)
			t.Cleanup(func() { _ = response.Body.Close() })
			state, err := store.GetState(t.Context(), buildKiroAccountKey(account))
			require.NoError(t, err)
			if tc.limited {
				require.NotNil(t, state)
				require.True(t, state.Active)
			} else {
				require.Nil(t, state, "model capacity does not make the account unavailable")
			}
		})
	}
}
