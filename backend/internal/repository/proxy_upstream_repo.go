package repository

// 链式代理（fork）：前置端点 / 前置组 / 槽位的读写，见迁移 257 与 service/proxy_upstream_model.go。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type proxyUpstreamRepository struct {
	db *sql.DB
}

func NewProxyUpstreamRepository(db *sql.DB) service.ProxyUpstreamRepository {
	return &proxyUpstreamRepository{db: db}
}

const proxyUpstreamEndpointColumns = `id, name, protocol, host, port, username, password, status, created_at, updated_at`

func scanProxyUpstreamEndpoint(scan func(dest ...any) error) (*service.ProxyUpstreamEndpoint, error) {
	var e service.ProxyUpstreamEndpoint
	if err := scan(&e.ID, &e.Name, &e.Protocol, &e.Host, &e.Port, &e.Username, &e.Password, &e.Status, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *proxyUpstreamRepository) ListEndpoints(ctx context.Context) ([]service.ProxyUpstreamEndpoint, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+proxyUpstreamEndpointColumns+` FROM proxy_upstream_endpoints ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.ProxyUpstreamEndpoint{}
	for rows.Next() {
		e, err := scanProxyUpstreamEndpoint(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (r *proxyUpstreamRepository) GetEndpoint(ctx context.Context, id int64) (*service.ProxyUpstreamEndpoint, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+proxyUpstreamEndpointColumns+` FROM proxy_upstream_endpoints WHERE id = $1`, id)
	e, err := scanProxyUpstreamEndpoint(row.Scan)
	return e, translatePersistenceError(err, service.ErrProxyUpstreamEndpointNotFound, nil)
}

func (r *proxyUpstreamRepository) CreateEndpoint(ctx context.Context, e *service.ProxyUpstreamEndpoint) error {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO proxy_upstream_endpoints (name, protocol, host, port, username, password, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+proxyUpstreamEndpointColumns,
		e.Name, e.Protocol, e.Host, e.Port, e.Username, e.Password, e.Status)
	created, err := scanProxyUpstreamEndpoint(row.Scan)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrProxyUpstreamEndpointNameExists)
	}
	*e = *created
	return nil
}

func (r *proxyUpstreamRepository) UpdateEndpoint(ctx context.Context, e *service.ProxyUpstreamEndpoint) error {
	row := r.db.QueryRowContext(ctx, `
		UPDATE proxy_upstream_endpoints
		SET name = $2, protocol = $3, host = $4, port = $5, username = $6, password = $7, status = $8, updated_at = NOW()
		WHERE id = $1
		RETURNING `+proxyUpstreamEndpointColumns,
		e.ID, e.Name, e.Protocol, e.Host, e.Port, e.Username, e.Password, e.Status)
	updated, err := scanProxyUpstreamEndpoint(row.Scan)
	if err != nil {
		return translatePersistenceError(err, service.ErrProxyUpstreamEndpointNotFound, service.ErrProxyUpstreamEndpointNameExists)
	}
	*e = *updated
	return nil
}

// DeleteEndpoint 在锁住槽位表后检查引用，避免并发新增引用后删掉端点。
func (r *proxyUpstreamRepository) DeleteEndpoint(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// 锁住全部槽位，与 UpdateGroup / CreateGroup 写槽位互斥。
	if _, err := tx.ExecContext(ctx, `LOCK TABLE proxy_upstream_slots IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM proxy_upstream_slots WHERE endpoint_ids @> to_jsonb($1::bigint))`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return service.ErrProxyUpstreamEndpointInUse
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM proxy_upstream_endpoints WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrProxyUpstreamEndpointNotFound
	}
	return tx.Commit()
}

const proxyUpstreamGroupColumns = `id, name, status, probe_url, probe_interval_seconds, fail_threshold, recover_threshold, failback_seconds, created_at, updated_at`

func scanProxyUpstreamGroup(scan func(dest ...any) error) (*service.ProxyUpstreamGroup, error) {
	var g service.ProxyUpstreamGroup
	if err := scan(&g.ID, &g.Name, &g.Status, &g.ProbeURL, &g.ProbeIntervalSeconds, &g.FailThreshold, &g.RecoverThreshold, &g.FailbackSeconds, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	g.Slots = []service.ProxyUpstreamSlot{}
	return &g, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadProxyUpstreamSlots(ctx context.Context, q queryer, groupIDs []int64) (map[int64][]service.ProxyUpstreamSlot, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, group_id, name, position, endpoint_ids, alert_event_id
		FROM proxy_upstream_slots WHERE group_id = ANY($1) ORDER BY group_id, position, id`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64][]service.ProxyUpstreamSlot{}
	for rows.Next() {
		var s service.ProxyUpstreamSlot
		var raw []byte
		var alert sql.NullInt64
		if err := rows.Scan(&s.ID, &s.GroupID, &s.Name, &s.Position, &raw, &alert); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &s.EndpointIDs); err != nil {
			return nil, fmt.Errorf("decode slot %d endpoint_ids: %w", s.ID, err)
		}
		if alert.Valid {
			v := alert.Int64
			s.AlertEventID = &v
		}
		out[s.GroupID] = append(out[s.GroupID], s)
	}
	return out, rows.Err()
}

func (r *proxyUpstreamRepository) ListGroups(ctx context.Context) ([]service.ProxyUpstreamGroup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+proxyUpstreamGroupColumns+` FROM proxy_upstream_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	groups := []service.ProxyUpstreamGroup{}
	for rows.Next() {
		g, err := scanProxyUpstreamGroup(rows.Scan)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		groups = append(groups, *g)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]int64, len(groups))
	for i := range groups {
		ids[i] = groups[i].ID
	}
	slots, err := loadProxyUpstreamSlots(ctx, r.db, ids)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if s := slots[groups[i].ID]; s != nil {
			groups[i].Slots = s
		}
	}
	return groups, nil
}

func (r *proxyUpstreamRepository) GetGroup(ctx context.Context, id int64) (*service.ProxyUpstreamGroup, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+proxyUpstreamGroupColumns+` FROM proxy_upstream_groups WHERE id = $1`, id)
	g, err := scanProxyUpstreamGroup(row.Scan)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrProxyUpstreamGroupNotFound, nil)
	}
	slots, err := loadProxyUpstreamSlots(ctx, r.db, []int64{id})
	if err != nil {
		return nil, err
	}
	if s := slots[id]; s != nil {
		g.Slots = s
	}
	return g, nil
}

func (r *proxyUpstreamRepository) CreateGroup(ctx context.Context, g *service.ProxyUpstreamGroup) error {
	return r.writeGroup(ctx, g, true)
}

func (r *proxyUpstreamRepository) UpdateGroup(ctx context.Context, g *service.ProxyUpstreamGroup) error {
	return r.writeGroup(ctx, g, false)
}

// writeGroup 在一个事务里写组与槽位：槽位顺序即 position；带 id 且属于该组的槽位原地更新（保留 id，
// 运行时状态与告警关联不丢），其余新建，未出现的删除。端点存在性在锁内复核，防止与删除端点并发。
func (r *proxyUpstreamRepository) writeGroup(ctx context.Context, g *service.ProxyUpstreamGroup, create bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `LOCK TABLE proxy_upstream_slots IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	endpointIDs := []int64{}
	for _, s := range g.Slots {
		endpointIDs = append(endpointIDs, s.EndpointIDs...)
	}
	var missing int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM unnest($1::bigint[]) AS x(id)
		WHERE NOT EXISTS (SELECT 1 FROM proxy_upstream_endpoints e WHERE e.id = x.id)`, pq.Array(endpointIDs)).Scan(&missing); err != nil {
		return err
	}
	if missing > 0 {
		return service.ErrProxyUpstreamEndpointNotFound
	}

	var row *sql.Row
	if create {
		row = tx.QueryRowContext(ctx, `
			INSERT INTO proxy_upstream_groups (name, status, probe_url, probe_interval_seconds, fail_threshold, recover_threshold, failback_seconds)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+proxyUpstreamGroupColumns,
			g.Name, g.Status, g.ProbeURL, g.ProbeIntervalSeconds, g.FailThreshold, g.RecoverThreshold, g.FailbackSeconds)
	} else {
		row = tx.QueryRowContext(ctx, `
			UPDATE proxy_upstream_groups
			SET name = $2, status = $3, probe_url = $4, probe_interval_seconds = $5, fail_threshold = $6,
			    recover_threshold = $7, failback_seconds = $8, updated_at = NOW()
			WHERE id = $1
			RETURNING `+proxyUpstreamGroupColumns,
			g.ID, g.Name, g.Status, g.ProbeURL, g.ProbeIntervalSeconds, g.FailThreshold, g.RecoverThreshold, g.FailbackSeconds)
	}
	saved, err := scanProxyUpstreamGroup(row.Scan)
	if err != nil {
		return translatePersistenceError(err, service.ErrProxyUpstreamGroupNotFound, service.ErrProxyUpstreamGroupNameExists)
	}

	keep := []int64{}
	for i := range g.Slots {
		s := &g.Slots[i]
		raw, _ := json.Marshal(s.EndpointIDs)
		position := i + 1
		updated := false
		if s.ID > 0 && !create {
			res, err := tx.ExecContext(ctx, `
				UPDATE proxy_upstream_slots SET name = $3, position = $4, endpoint_ids = $5, updated_at = NOW()
				WHERE id = $1 AND group_id = $2`, s.ID, saved.ID, s.Name, position, raw)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			updated = n > 0
		}
		if !updated {
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO proxy_upstream_slots (group_id, name, position, endpoint_ids)
				VALUES ($1, $2, $3, $4) RETURNING id`, saved.ID, s.Name, position, raw).Scan(&s.ID); err != nil {
				return err
			}
		}
		keep = append(keep, s.ID)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM proxy_upstream_slots WHERE group_id = $1 AND NOT (id = ANY($2))`, saved.ID, pq.Array(keep)); err != nil {
		return err
	}
	slots, err := loadProxyUpstreamSlots(ctx, tx, []int64{saved.ID})
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	saved.Slots = slots[saved.ID]
	if saved.Slots == nil {
		saved.Slots = []service.ProxyUpstreamSlot{}
	}
	*g = *saved
	return nil
}

// DeleteGroup 拒绝删除仍被未删除代理引用的组。软删除的代理不钉住组（外键 ON DELETE SET NULL）；
// 并发新增的引用会被置空，代理保持 group 模式而组 id 为空，解析时 fail-closed。
func (r *proxyUpstreamRepository) DeleteGroup(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM proxy_upstream_groups g WHERE g.id = $1
		AND NOT EXISTS (SELECT 1 FROM proxies p WHERE p.deleted_at IS NULL AND p.upstream_group_id = g.id)`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM proxy_upstream_groups WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return service.ErrProxyUpstreamGroupInUse
	}
	return service.ErrProxyUpstreamGroupNotFound
}

func (r *proxyUpstreamRepository) SetSlotAlertEventID(ctx context.Context, slotID int64, alertEventID *int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE proxy_upstream_slots SET alert_event_id = $2 WHERE id = $1`, slotID, alertEventID)
	return err
}
