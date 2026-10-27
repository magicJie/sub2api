package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/proxy"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// 代理池（fork）：proxy_pools / proxy_account_bindings 的读写，见迁移 248 与 service/proxy_pool.go。
type proxyPoolRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

func NewProxyPoolRepository(client *dbent.Client, sqlDB *sql.DB) service.ProxyPoolRepository {
	return &proxyPoolRepository{client: client, sql: sqlDB}
}

const proxyPoolColumns = `p.id, p.name, p.description, p.platform_limits, p.is_default, p.created_at, p.updated_at`

func scanProxyPool(scan func(dest ...any) error, extra ...any) (*service.ProxyPool, error) {
	var pool service.ProxyPool
	var limits []byte
	dest := append([]any{&pool.ID, &pool.Name, &pool.Description, &limits, &pool.IsDefault, &pool.CreatedAt, &pool.UpdatedAt}, extra...)
	if err := scan(dest...); err != nil {
		return nil, err
	}
	pool.PlatformLimits = map[string]int{}
	if len(limits) > 0 {
		if err := json.Unmarshal(limits, &pool.PlatformLimits); err != nil {
			return nil, err
		}
	}
	return &pool, nil
}

func marshalPlatformLimits(limits map[string]int) ([]byte, error) {
	if limits == nil {
		limits = map[string]int{}
	}
	return json.Marshal(limits)
}

func (r *proxyPoolRepository) ListPools(ctx context.Context) ([]service.ProxyPool, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT `+proxyPoolColumns+`,
			(SELECT COUNT(*) FROM proxies x
			 WHERE x.deleted_at IS NULL AND x.role = 'egress'
			   AND (x.pool_id = p.id OR (p.is_default AND x.pool_id IS NULL)))
		FROM proxy_pools p
		ORDER BY p.is_default DESC, p.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.ProxyPool, 0)
	for rows.Next() {
		var count int64
		pool, err := scanProxyPool(rows.Scan, &count)
		if err != nil {
			return nil, err
		}
		pool.ProxyCount = count
		out = append(out, *pool)
	}
	return out, rows.Err()
}

func (r *proxyPoolRepository) GetPool(ctx context.Context, id int64) (*service.ProxyPool, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+proxyPoolColumns+` FROM proxy_pools p WHERE p.id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrProxyPoolNotFound
	}
	return scanProxyPool(rows.Scan)
}

func (r *proxyPoolRepository) CreatePool(ctx context.Context, pool *service.ProxyPool) error {
	limits, err := marshalPlatformLimits(pool.PlatformLimits)
	if err != nil {
		return err
	}
	rows, err := r.sql.QueryContext(ctx, `
		INSERT INTO proxy_pools (name, description, platform_limits)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`, pool.Name, pool.Description, limits)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrProxyPoolNameExists)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return rows.Err()
	}
	return rows.Scan(&pool.ID, &pool.CreatedAt, &pool.UpdatedAt)
}

func (r *proxyPoolRepository) UpdatePool(ctx context.Context, pool *service.ProxyPool) error {
	limits, err := marshalPlatformLimits(pool.PlatformLimits)
	if err != nil {
		return err
	}
	res, err := r.sql.ExecContext(ctx, `
		UPDATE proxy_pools SET name = $2, description = $3, platform_limits = $4, updated_at = NOW()
		WHERE id = $1`, pool.ID, pool.Name, pool.Description, limits)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrProxyPoolNameExists)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrProxyPoolNotFound
	}
	pool.UpdatedAt = time.Now()
	return nil
}

// DeletePool 删除非默认代理池；其代理经外键 ON DELETE SET NULL 回到默认池。
func (r *proxyPoolRepository) DeletePool(ctx context.Context, id int64) error {
	res, err := r.sql.ExecContext(ctx, `DELETE FROM proxy_pools WHERE id = $1 AND NOT is_default`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrProxyPoolNotFound
	}
	return nil
}

func (r *proxyPoolRepository) AssignProxies(ctx context.Context, pool *service.ProxyPool, proxyIDs []int64) error {
	if len(proxyIDs) == 0 {
		return nil
	}
	var poolID any
	if !pool.IsDefault {
		poolID = pool.ID
	}
	_, err := r.sql.ExecContext(ctx, `
		UPDATE proxies SET pool_id = $1, updated_at = NOW()
		WHERE id = ANY($2) AND deleted_at IS NULL AND role = 'egress'`, poolID, pq.Array(proxyIDs))
	if err == nil {
		service.RefreshProxyChainSnapshot(ctx)
	}
	return err
}

func (r *proxyPoolRepository) ListMembers(ctx context.Context, pool *service.ProxyPool, excludeAccountID int64) ([]service.ProxyPoolMember, error) {
	// 只有落地代理（role=egress）属于代理池；transport 只做传输。
	q := clientFromContext(ctx, r.client).Proxy.Query().Where(proxy.RoleEQ(service.ProxyRoleEgress))
	if pool.IsDefault {
		q = q.Where(proxy.Or(proxy.PoolIDIsNil(), proxy.PoolIDEQ(pool.ID)))
	} else {
		q = q.Where(proxy.PoolIDEQ(pool.ID))
	}
	proxies, err := q.Order(dbent.Asc(proxy.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	members := make([]service.ProxyPoolMember, 0, len(proxies))
	index := make(map[int64]int, len(proxies))
	ids := make([]int64, 0, len(proxies))
	for _, p := range proxies {
		index[p.ID] = len(members)
		ids = append(ids, p.ID)
		members = append(members, service.ProxyPoolMember{
			Proxy:         *proxyEntityToService(p),
			HistoryCounts: map[string]int64{},
			ActiveCounts:  map[string]int64{},
		})
	}
	if len(ids) == 0 {
		return members, nil
	}
	// 计数按落地 IP（host）合并：同一 IP 的其他代理记录（含已删除、重复登记）的绑定都算在内，
	// 删掉重建或重复登记都绕不过平台上限。
	rows, err := r.sql.QueryContext(ctx, `
		SELECT m.id, b.platform,
			COUNT(DISTINCT b.account_id),
			COUNT(DISTINCT b.account_id) FILTER (WHERE b.unbound_at IS NULL)
		FROM proxies m
		JOIN proxies px ON lower(px.host) = lower(m.host)
		JOIN proxy_account_bindings b ON b.proxy_id = px.id
		WHERE m.id = ANY($1) AND b.account_id <> $2
		GROUP BY m.id, b.platform`, pq.Array(ids), excludeAccountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var proxyID, total, active int64
		var platform string
		if err := rows.Scan(&proxyID, &platform, &total, &active); err != nil {
			return nil, err
		}
		if i, ok := index[proxyID]; ok {
			members[i].HistoryCounts[platform] = total
			members[i].ActiveCounts[platform] = active
		}
	}
	return members, rows.Err()
}

const proxyBindingSelect = `
	SELECT b.id, b.proxy_id, COALESCE(px.name, ''), b.account_id, COALESCE(a.name, ''), b.platform,
		b.pool_id, COALESCE(pp.name, ''), b.source, b.locked, b.bound_at, b.unlocked_at, b.unbound_at,
		COALESCE(b.unbind_reason, '')
	FROM proxy_account_bindings b
	LEFT JOIN proxies px ON px.id = b.proxy_id
	LEFT JOIN accounts a ON a.id = b.account_id
	LEFT JOIN proxy_pools pp ON pp.id = b.pool_id`

func scanProxyBindings(rows *sql.Rows) ([]service.ProxyAccountBinding, error) {
	defer func() { _ = rows.Close() }()
	out := make([]service.ProxyAccountBinding, 0)
	for rows.Next() {
		var b service.ProxyAccountBinding
		var poolID sql.NullInt64
		var unlockedAt, unboundAt sql.NullTime
		if err := rows.Scan(&b.ID, &b.ProxyID, &b.ProxyName, &b.AccountID, &b.AccountName, &b.Platform,
			&poolID, &b.PoolName, &b.Source, &b.Locked, &b.BoundAt, &unlockedAt, &unboundAt, &b.UnbindReason); err != nil {
			return nil, err
		}
		if poolID.Valid {
			v := poolID.Int64
			b.PoolID = &v
		}
		if unlockedAt.Valid {
			v := unlockedAt.Time
			b.UnlockedAt = &v
		}
		if unboundAt.Valid {
			v := unboundAt.Time
			b.UnboundAt = &v
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *proxyPoolRepository) ListBindingsByProxy(ctx context.Context, proxyID int64) ([]service.ProxyAccountBinding, error) {
	rows, err := r.sql.QueryContext(ctx, proxyBindingSelect+`
		WHERE b.proxy_id = $1
		ORDER BY (b.unbound_at IS NULL) DESC, b.bound_at DESC, b.id DESC`, proxyID)
	if err != nil {
		return nil, err
	}
	return scanProxyBindings(rows)
}

func (r *proxyPoolRepository) GetOpenBinding(ctx context.Context, accountID int64) (*service.ProxyAccountBinding, error) {
	rows, err := r.sql.QueryContext(ctx, proxyBindingSelect+`
		WHERE b.account_id = $1 AND b.unbound_at IS NULL
		ORDER BY b.id DESC LIMIT 1`, accountID)
	if err != nil {
		return nil, err
	}
	bindings, err := scanProxyBindings(rows)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, service.ErrProxyPoolBindingNotFound
	}
	return &bindings[0], nil
}

func (r *proxyPoolRepository) LockBinding(ctx context.Context, accountID, poolID int64) error {
	res, err := r.sql.ExecContext(ctx, `
		UPDATE proxy_account_bindings
		SET source = $3, locked = TRUE, pool_id = $2, unlocked_at = NULL
		WHERE account_id = $1 AND unbound_at IS NULL`, accountID, poolID, service.ProxyBindingSourcePool)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrProxyPoolBindingNotFound
	}
	return nil
}

func (r *proxyPoolRepository) UnlockBinding(ctx context.Context, accountID int64) error {
	res, err := r.sql.ExecContext(ctx, `
		UPDATE proxy_account_bindings SET locked = FALSE, unlocked_at = NOW()
		WHERE account_id = $1 AND unbound_at IS NULL AND locked`, accountID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrProxyPoolBindingNotFound
	}
	return nil
}

func (r *proxyPoolRepository) ListLockedAccountIDs(ctx context.Context, accountIDs []int64) ([]int64, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT account_id FROM proxy_account_bindings
		WHERE account_id = ANY($1) AND unbound_at IS NULL AND locked
		ORDER BY account_id`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}

func (r *proxyPoolRepository) EgressHostTaken(ctx context.Context, host string, excludeProxyID int64) (bool, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT 1 FROM proxies
		WHERE deleted_at IS NULL AND role = $1 AND lower(host) = lower($2) AND id <> $3
		LIMIT 1`, service.ProxyRoleEgress, host, excludeProxyID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}
