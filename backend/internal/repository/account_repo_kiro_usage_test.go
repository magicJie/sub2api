package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 快照写入只改 extra 的一个键：不碰 updated_at、不写调度 outbox（sqlmock 对未声明的语句会报错）。
func TestUpdateKiroUsageSnapshotOnlyTouchesSnapshotKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, 10, 27, 12, 0, 0, 123, time.UTC)
	expected := now.Add(-15 * time.Minute)
	snapshot := &service.KiroUsageSnapshot{UpdatedAt: &now, LastAttemptAt: now}
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)

	mock.ExpectExec(`(?s)UPDATE accounts\s+SET extra = COALESCE\(extra, '\{\}'::jsonb\) \|\| jsonb_build_object\('kiro_usage_snapshot', \$1::jsonb\)\s+WHERE id = \$2`).
		WithArgs(string(encoded), int64(7), expected.Format(time.RFC3339Nano)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	require.NoError(t, repo.UpdateKiroUsageSnapshot(context.Background(), 7, snapshot, &expected))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateKiroUsageSnapshotStaleWhenNoRowMatches(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	mock.ExpectExec(`(?s)UPDATE accounts.*kiro_usage_snapshot`).
		WithArgs(sqlmock.AnyArg(), int64(8), nil).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	err = repo.UpdateKiroUsageSnapshot(context.Background(), 8, &service.KiroUsageSnapshot{LastAttemptAt: now}, nil)
	require.ErrorIs(t, err, service.ErrKiroUsageSnapshotStale)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListKiroUsageRefreshCandidatesDecodesSnapshots(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	lastUsed := time.Date(2026, 10, 27, 11, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)SELECT id, last_used_at.*FROM accounts.*platform = 'kiro'.*type = 'oauth'.*status = 'active'`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "last_used_at", "snapshot"}).
			AddRow(int64(1), lastUsed, `{"updated_at":"2026-10-27T11:30:00Z","last_attempt_at":"2026-10-27T11:30:00Z","kiro_credit":{"current_usage":10,"usage_limit":100,"percentage_used":10}}`).
			AddRow(int64(2), nil, `null`))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	candidates, err := repo.ListKiroUsageRefreshCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, int64(1), candidates[0].ID)
	require.True(t, candidates[0].LastUsedAt.Equal(lastUsed))
	require.NotNil(t, candidates[0].Snapshot)
	require.InDelta(t, 10, candidates[0].Snapshot.KiroCredit.PercentageUsed, 0.001)
	require.Nil(t, candidates[1].Snapshot)
	require.Nil(t, candidates[1].LastUsedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRequestKiroUsageRefreshAllMarksExistingSnapshots(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec(`(?s)UPDATE accounts\s+SET extra = jsonb_set\(.*refresh_requested_at`).
		WithArgs(at.Format(time.RFC3339Nano)).
		WillReturnResult(sqlmock.NewResult(0, 3))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	n, err := repo.RequestKiroUsageRefreshAll(context.Background(), at)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
	require.NoError(t, mock.ExpectationsWereMet())
}
