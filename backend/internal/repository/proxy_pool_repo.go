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

// proxy_pools.platform_limits 自迁移 251 起由 platform_ip_policies 取代，只保留供回滚，不再读写。
const proxyPoolColumns = `p.id, p.name, p.description, p.is_default, p.created_at, p.updated_at`

func scanProxyPool(scan func(dest ...any) error, extra ...any) (*service.ProxyPool, error) {
	var pool service.ProxyPool
	dest := append([]any{&pool.ID, &pool.Name, &pool.Description, &pool.IsDefault, &pool.CreatedAt, &pool.UpdatedAt}, extra...)
	if err := scan(dest...); err != nil {
		return nil, err
	}
	return &pool, nil
}

func (r *proxyPoolRepository) ListPools(ctx context.Context) ([]service.ProxyPool, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT `+proxyPoolColumns+`,
			(SELECT COUNT(*) FROM proxies x
			 WHERE x.deleted_at IS NULL
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
	rows, err := r.sql.QueryContext(ctx, `
		INSERT INTO proxy_pools (name, description)
		VALUES ($1, $2)
		RETURNING id, created_at, updated_at`, pool.Name, pool.Description)
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
	res, err := r.sql.ExecContext(ctx, `
		UPDATE proxy_pools SET name = $2, description = $3, updated_at = NOW()
		WHERE id = $1`, pool.ID, pool.Name, pool.Description)
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
		WHERE id = ANY($2) AND deleted_at IS NULL`, poolID, pq.Array(proxyIDs))
	if err == nil {
		service.RefreshProxyChainSnapshot(ctx)
	}
	return err
}

func (r *proxyPoolRepository) ListMembers(ctx context.Context, pool *service.ProxyPool, excludeAccountID int64) ([]service.ProxyPoolMember, error) {
	q := clientFromContext(ctx, r.client).Proxy.Query()
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
		WHERE deleted_at IS NULL AND lower(host) = lower($1) AND id <> $2
		LIMIT 1`, host, excludeProxyID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}

// ---------------------------------------------------------------------------
// 平台 IP 策略（迁移 251）
// ---------------------------------------------------------------------------

func (r *proxyPoolRepository) ListPolicies(ctx context.Context) ([]service.ProxyIPPolicy, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, platform, mode, pool_ids, max_lifetime_accounts, max_active_accounts, on_egress_failure, created_at, updated_at
		FROM platform_ip_policies ORDER BY platform`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.ProxyIPPolicy, 0)
	for rows.Next() {
		var p service.ProxyIPPolicy
		var poolIDs []byte
		var lifetime, active sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Platform, &p.Mode, &poolIDs, &lifetime, &active, &p.OnEgressFailure, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.PoolIDs = []int64{}
		if len(poolIDs) > 0 {
			if err := json.Unmarshal(poolIDs, &p.PoolIDs); err != nil {
				return nil, err
			}
		}
		if lifetime.Valid {
			v := int(lifetime.Int64)
			p.MaxLifetimeAccounts = &v
		}
		if active.Valid {
			v := int(active.Int64)
			p.MaxActiveAccounts = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *proxyPoolRepository) UpsertPolicy(ctx context.Context, p *service.ProxyIPPolicy) error {
	ids := p.PoolIDs
	if ids == nil {
		ids = []int64{}
	}
	poolIDs, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	rows, err := r.sql.QueryContext(ctx, `
		INSERT INTO platform_ip_policies (platform, mode, pool_ids, max_lifetime_accounts, max_active_accounts, on_egress_failure)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (platform) DO UPDATE SET
			mode = EXCLUDED.mode, pool_ids = EXCLUDED.pool_ids,
			max_lifetime_accounts = EXCLUDED.max_lifetime_accounts, max_active_accounts = EXCLUDED.max_active_accounts,
			on_egress_failure = EXCLUDED.on_egress_failure, updated_at = NOW()
		RETURNING id, created_at, updated_at`,
		p.Platform, p.Mode, poolIDs, p.MaxLifetimeAccounts, p.MaxActiveAccounts, p.OnEgressFailure)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return rows.Err()
	}
	return rows.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *proxyPoolRepository) DeletePolicy(ctx context.Context, platform string) error {
	res, err := r.sql.ExecContext(ctx, `DELETE FROM platform_ip_policies WHERE platform = $1`, platform)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrProxyIPPolicyNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// 落地 IP 故障与处置（迁移 252）
// ---------------------------------------------------------------------------

func (r *proxyPoolRepository) ListProxyAccountIDs(ctx context.Context, proxyID int64) ([]int64, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id FROM accounts
		WHERE proxy_id = $1 AND deleted_at IS NULL AND parent_account_id IS NULL
		ORDER BY id`, proxyID)
	if err != nil {
		return nil, err
	}
	return scanInt64Rows(rows)
}

func (r *proxyPoolRepository) ListOpenIncidentProxyIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT proxy_id FROM proxy_egress_incidents WHERE status = 'open' ORDER BY proxy_id`)
	if err != nil {
		return nil, err
	}
	return scanInt64Rows(rows)
}

func scanInt64Rows(rows *sql.Rows) ([]int64, error) {
	defer func() { _ = rows.Close() }()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const proxyIncidentColumns = `i.id, i.proxy_id, COALESCE(px.name, ''), i.layer, i.status, i.last_error, i.fail_count,
	i.first_failed_at, i.last_checked_at, i.resolved_at, i.handled_at, i.alert_event_id`

func scanProxyIncident(scan func(dest ...any) error) (*service.ProxyEgressIncident, error) {
	var in service.ProxyEgressIncident
	var resolvedAt, handledAt sql.NullTime
	var alertID sql.NullInt64
	if err := scan(&in.ID, &in.ProxyID, &in.ProxyName, &in.Layer, &in.Status, &in.LastError, &in.FailCount,
		&in.FirstFailedAt, &in.LastCheckedAt, &resolvedAt, &handledAt, &alertID); err != nil {
		return nil, err
	}
	if resolvedAt.Valid {
		in.ResolvedAt = &resolvedAt.Time
	}
	if handledAt.Valid {
		in.HandledAt = &handledAt.Time
	}
	if alertID.Valid {
		in.AlertEventID = &alertID.Int64
	}
	return &in, nil
}

func (r *proxyPoolRepository) GetOpenIncident(ctx context.Context, proxyID int64) (*service.ProxyEgressIncident, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+proxyIncidentColumns+`
		FROM proxy_egress_incidents i LEFT JOIN proxies px ON px.id = i.proxy_id
		WHERE i.proxy_id = $1 AND i.status = 'open'`, proxyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanProxyIncident(rows.Scan)
}

// RecordIncidentFailure 记一次探测失败：没有未恢复的故障则新建，否则累加连续失败次数并更新层级与错误。
func (r *proxyPoolRepository) RecordIncidentFailure(ctx context.Context, proxyID int64, layer, lastError string) (*service.ProxyEgressIncident, error) {
	rows, err := r.sql.QueryContext(ctx, `
		WITH up AS (
			INSERT INTO proxy_egress_incidents (proxy_id, layer, last_error)
			VALUES ($1, $2, $3)
			ON CONFLICT (proxy_id) WHERE status = 'open' DO UPDATE SET
				-- 故障层级变化（如前置恢复后落地不通）时重新计数并重新处置。
				fail_count = CASE WHEN proxy_egress_incidents.layer = EXCLUDED.layer
					THEN proxy_egress_incidents.fail_count + 1 ELSE 1 END,
				handled_at = CASE WHEN proxy_egress_incidents.layer = EXCLUDED.layer
					THEN proxy_egress_incidents.handled_at ELSE NULL END,
				layer = EXCLUDED.layer, last_error = EXCLUDED.last_error, last_checked_at = NOW()
			RETURNING *
		)
		SELECT `+proxyIncidentColumns+` FROM up i LEFT JOIN proxies px ON px.id = i.proxy_id`, proxyID, layer, lastError)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	return scanProxyIncident(rows.Scan)
}

func (r *proxyPoolRepository) MarkIncidentHandled(ctx context.Context, incidentID int64, alertEventID *int64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE proxy_egress_incidents SET handled_at = NOW(), alert_event_id = COALESCE($2, alert_event_id)
		WHERE id = $1`, incidentID, alertEventID)
	return err
}

func (r *proxyPoolRepository) ResolveIncident(ctx context.Context, incidentID int64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE proxy_egress_incidents SET status = 'resolved', resolved_at = NOW(), last_checked_at = NOW()
		WHERE id = $1 AND status = 'open'`, incidentID)
	return err
}

func (r *proxyPoolRepository) RecordIncidentAccount(ctx context.Context, rec *service.ProxyEgressIncidentAccount) error {
	_, err := r.sql.ExecContext(ctx, `
		INSERT INTO proxy_egress_incident_accounts (incident_id, account_id, action, to_proxy_id, message)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (incident_id, account_id) DO NOTHING`,
		rec.IncidentID, rec.AccountID, rec.Action, rec.ToProxyID, rec.Message)
	return err
}

// GetPausedIncidentAccount 返回账号当前因落地 IP 故障被暂停、尚未恢复的处置记录。
func (r *proxyPoolRepository) GetPausedIncidentAccount(ctx context.Context, accountID int64) (*service.ProxyEgressIncidentAccount, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, incident_id, account_id, action, to_proxy_id, message, created_at, resumed_at
		FROM proxy_egress_incident_accounts
		WHERE account_id = $1 AND action = 'paused' AND resumed_at IS NULL
		ORDER BY id DESC LIMIT 1`, accountID)
	if err != nil {
		return nil, err
	}
	recs, err := scanIncidentAccounts(rows)
	if err != nil || len(recs) == 0 {
		return nil, err
	}
	return &recs[0], nil
}

// MarkAccountResumed 标记账号的所有未恢复处置记录已处理（人工恢复或换 IP）。
func (r *proxyPoolRepository) MarkAccountResumed(ctx context.Context, accountID int64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE proxy_egress_incident_accounts SET resumed_at = NOW()
		WHERE account_id = $1 AND resumed_at IS NULL`, accountID)
	return err
}

func scanIncidentAccounts(rows *sql.Rows) ([]service.ProxyEgressIncidentAccount, error) {
	defer func() { _ = rows.Close() }()
	out := make([]service.ProxyEgressIncidentAccount, 0)
	for rows.Next() {
		var a service.ProxyEgressIncidentAccount
		var toProxy sql.NullInt64
		var resumedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.IncidentID, &a.AccountID, &a.Action, &toProxy, &a.Message, &a.CreatedAt, &resumedAt); err != nil {
			return nil, err
		}
		if toProxy.Valid {
			a.ToProxyID = &toProxy.Int64
		}
		if resumedAt.Valid {
			a.ResumedAt = &resumedAt.Time
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListIncidents 返回未恢复的故障，以及最近 resolvedLimit 条已恢复的故障，附带账号处置记录。
func (r *proxyPoolRepository) ListIncidents(ctx context.Context, resolvedLimit int) ([]service.ProxyEgressIncident, error) {
	rows, err := r.sql.QueryContext(ctx, `
		(SELECT `+proxyIncidentColumns+` FROM proxy_egress_incidents i LEFT JOIN proxies px ON px.id = i.proxy_id
		 WHERE i.status = 'open')
		UNION ALL
		(SELECT `+proxyIncidentColumns+` FROM proxy_egress_incidents i LEFT JOIN proxies px ON px.id = i.proxy_id
		 WHERE i.status = 'resolved' ORDER BY i.resolved_at DESC LIMIT $1)`, resolvedLimit)
	if err != nil {
		return nil, err
	}
	out := make([]service.ProxyEgressIncident, 0)
	index := map[int64]int{}
	ids := make([]int64, 0)
	func() {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			in, scanErr := scanProxyIncident(rows.Scan)
			if scanErr != nil {
				err = scanErr
				return
			}
			in.Accounts = []service.ProxyEgressIncidentAccount{}
			index[in.ID] = len(out)
			ids = append(ids, in.ID)
			out = append(out, *in)
		}
		err = rows.Err()
	}()
	if err != nil || len(ids) == 0 {
		return out, err
	}
	arows, err := r.sql.QueryContext(ctx, `
		SELECT ia.id, ia.incident_id, ia.account_id, ia.action, ia.to_proxy_id, ia.message, ia.created_at, ia.resumed_at,
			COALESCE(a.name, ''), COALESCE(a.platform, ''), COALESCE(a.schedulable, FALSE), a.proxy_id
		FROM proxy_egress_incident_accounts ia
		LEFT JOIN accounts a ON a.id = ia.account_id
		WHERE ia.incident_id = ANY($1)
		ORDER BY ia.id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = arows.Close() }()
	for arows.Next() {
		var a service.ProxyEgressIncidentAccount
		var toProxy, curProxy sql.NullInt64
		var resumedAt sql.NullTime
		if err := arows.Scan(&a.ID, &a.IncidentID, &a.AccountID, &a.Action, &toProxy, &a.Message, &a.CreatedAt, &resumedAt,
			&a.AccountName, &a.Platform, &a.Schedulable, &curProxy); err != nil {
			return nil, err
		}
		if toProxy.Valid {
			a.ToProxyID = &toProxy.Int64
		}
		if resumedAt.Valid {
			a.ResumedAt = &resumedAt.Time
		}
		if curProxy.Valid {
			a.CurrentProxyID = &curProxy.Int64
		}
		if i, ok := index[a.IncidentID]; ok {
			out[i].Accounts = append(out[i].Accounts, a)
		}
	}
	return out, arows.Err()
}
