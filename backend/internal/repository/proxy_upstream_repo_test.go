//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

var proxyUpstreamAIORowColumns = []string{"id", "name", "protocol", "host", "username", "password", "status", "created_at", "updated_at"}

// 修改 AIO 时把主机、协议与认证同步到端口冗余列（回滚到旧版本时链路不变）。
func TestProxyUpstreamUpdateAIOSyncsEndpointColumns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE proxy_upstream_aios")).
		WithArgs(int64(3), "aio", "http", "10.0.0.9", "u", "p", "active").
		WillReturnRows(sqlmock.NewRows(proxyUpstreamAIORowColumns).AddRow(int64(3), "aio", "http", "10.0.0.9", "u", "p", "active", now, now))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE proxy_upstream_endpoints SET protocol = $2, host = $3, username = $4, password = $5")).
		WithArgs(int64(3), "http", "10.0.0.9", "u", "p").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	a := &service.ProxyUpstreamAIO{ID: 3, Name: "aio", Protocol: "http", Host: "10.0.0.9", Username: "u", Password: "p", Status: "active"}
	require.NoError(t, NewProxyUpstreamRepository(db).UpdateAIO(context.Background(), a))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyUpstreamDeleteAIOInUse(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM proxy_upstream_aios")).WithArgs(int64(3)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS (SELECT 1 FROM proxy_upstream_aios")).WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	err = NewProxyUpstreamRepository(db).DeleteAIO(context.Background(), 3)
	require.True(t, errors.Is(err, service.ErrProxyUpstreamAIOInUse), "err=%v", err)

	// 并发新增端口：外键拒绝删除，同样报 in use。
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM proxy_upstream_aios")).WithArgs(int64(3)).WillReturnError(&pq.Error{Code: "23503"})
	err = NewProxyUpstreamRepository(db).DeleteAIO(context.Background(), 3)
	require.True(t, errors.Is(err, service.ErrProxyUpstreamAIOInUse), "err=%v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyUpstreamCreateEndpointRequiresAIO(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM proxy_upstream_aios WHERE id = $1 FOR UPDATE")).WithArgs(int64(9)).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	err = NewProxyUpstreamRepository(db).CreateEndpoint(context.Background(), &service.ProxyUpstreamEndpoint{AIOID: 9, Name: "xyy", Port: 17911, Status: "active"})
	require.True(t, errors.Is(err, service.ErrProxyUpstreamAIONotFound), "err=%v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 端口的协议、主机与认证取自 AIO（JOIN），插入时从 AIO 复制冗余列。
func TestProxyUpstreamCreateEndpointCopiesAIOFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM proxy_upstream_aios WHERE id = $1 FOR UPDATE")).WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(2)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT a.id, $2, a.protocol, a.host, $3, a.username, a.password, $4 FROM proxy_upstream_aios a WHERE a.id = $1")).
		WithArgs(int64(2), "bwg", 17912, "active").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(regexp.QuoteMeta("JOIN proxy_upstream_aios a ON a.id = e.aio_id WHERE e.id = $1")).WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "aio_id", "aio_name", "aio_status", "name", "protocol", "host", "port", "username", "password", "status", "created_at", "updated_at"}).
			AddRow(int64(7), int64(2), "aio", "active", "bwg", "socks5", "aio-host", 17912, "", "", "active", now, now))
	mock.ExpectCommit()
	e := &service.ProxyUpstreamEndpoint{AIOID: 2, Name: "bwg", Port: 17912, Status: "active"}
	require.NoError(t, NewProxyUpstreamRepository(db).CreateEndpoint(context.Background(), e))
	require.Equal(t, service.ProxyUpstreamEndpoint{ID: 7, AIOID: 2, AIOName: "aio", AIOStatus: "active", Name: "bwg", Protocol: "socks5",
		Host: "aio-host", Port: 17912, Status: "active", CreatedAt: now, UpdatedAt: now}, *e)
	require.NoError(t, mock.ExpectationsWereMet())
}
