//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestKiroRouteLogKeepsNewestFirstWithinCapacity(t *testing.T) {
	log := &kiroRouteLog{records: make([]KiroRouteRecord, 3)}
	for id := int64(1); id <= 5; id++ {
		log.add(KiroRouteRecord{AccountID: id})
	}

	got := log.recent(0)
	require.Len(t, got, 3)
	require.Equal(t, []int64{5, 4, 3}, []int64{got[0].AccountID, got[1].AccountID, got[2].AccountID})
	require.Len(t, log.recent(2), 2)
}

func TestRecordKiroRouteClassifiesOutcome(t *testing.T) {
	prev := globalKiroRouteLog
	globalKiroRouteLog = &kiroRouteLog{records: make([]KiroRouteRecord, 8)}
	t.Cleanup(func() { globalKiroRouteLog = prev })

	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "req-1")
	account := &Account{ID: 42, Name: "kiro-a", Platform: PlatformKiro}
	parsed := &ParsedRequest{Model: "claude-sonnet-4-6", Stream: true}
	start := time.Now()

	recordKiroRoute(ctx, "messages", account, parsed, start, &UpstreamFailoverError{StatusCode: http.StatusTooManyRequests})
	recordKiroRoute(ctx, "messages", account, parsed, start, errors.New("boom"))
	recordKiroRoute(ctx, "messages", account, parsed, start, nil)

	got := KiroRecentRoutes(0)
	require.Len(t, got, 3)
	require.Equal(t, "ok", got[0].Outcome)
	require.Equal(t, "error", got[1].Outcome)
	require.Equal(t, "failover", got[2].Outcome)
	require.Equal(t, http.StatusTooManyRequests, got[2].StatusCode)
	require.Equal(t, "req-1", got[2].RequestID)
	require.Equal(t, "claude-sonnet-4-6", got[2].Model)
	require.True(t, got[2].Stream)
}
