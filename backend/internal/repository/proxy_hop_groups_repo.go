package repository

// 链式代理（fork）：首跳组仓储与代理引用的持久化。

import (
	"context"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/proxyhopgroup"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type proxyHopGroupRepository struct{ client *dbent.Client }

func NewProxyHopGroupRepository(client *dbent.Client) service.ProxyHopGroupRepository {
	return &proxyHopGroupRepository{client: client}
}

func hopGroupFromEnt(group *dbent.ProxyHopGroup) *service.ProxyHopGroup {
	return &service.ProxyHopGroup{ID: group.ID, Name: group.Name, Strategy: group.Strategy, TestURL: group.TestURL, IntervalSeconds: group.IntervalSeconds, ToleranceMS: group.ToleranceMs, SubscriptionURLs: group.SubscriptionUrls, MemberProxyIDs: group.MemberProxyIds, SubscriptionUpdateInterval: group.SubscriptionUpdateInterval, Status: group.Status, CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt}
}

func (r *proxyHopGroupRepository) List(ctx context.Context) ([]service.ProxyHopGroup, error) {
	rows, err := r.client.ProxyHopGroup.Query().Order(dbent.Asc(proxyhopgroup.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	groups := make([]service.ProxyHopGroup, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, *hopGroupFromEnt(row))
	}
	return groups, nil
}

func (r *proxyHopGroupRepository) Get(ctx context.Context, id int64) (*service.ProxyHopGroup, error) {
	row, err := r.client.ProxyHopGroup.Get(ctx, id)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrProxyHopGroupNotFound, nil)
	}
	return hopGroupFromEnt(row), nil
}

func (r *proxyHopGroupRepository) Create(ctx context.Context, group *service.ProxyHopGroup) error {
	row, err := r.client.ProxyHopGroup.Create().SetName(group.Name).SetStrategy(group.Strategy).SetTestURL(group.TestURL).SetIntervalSeconds(group.IntervalSeconds).SetToleranceMs(group.ToleranceMS).SetSubscriptionUrls(group.SubscriptionURLs).SetMemberProxyIds(group.MemberProxyIDs).SetSubscriptionUpdateInterval(group.SubscriptionUpdateInterval).SetStatus(group.Status).Save(ctx)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrProxyHopGroupNameExists)
	}
	*group = *hopGroupFromEnt(row)
	return nil
}

func (r *proxyHopGroupRepository) Update(ctx context.Context, group *service.ProxyHopGroup) error {
	row, err := r.client.ProxyHopGroup.UpdateOneID(group.ID).SetName(group.Name).SetStrategy(group.Strategy).SetTestURL(group.TestURL).SetIntervalSeconds(group.IntervalSeconds).SetToleranceMs(group.ToleranceMS).SetSubscriptionUrls(group.SubscriptionURLs).SetMemberProxyIds(group.MemberProxyIDs).SetSubscriptionUpdateInterval(group.SubscriptionUpdateInterval).SetStatus(group.Status).Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrProxyHopGroupNotFound, service.ErrProxyHopGroupNameExists)
	}
	*group = *hopGroupFromEnt(row)
	return nil
}

func (r *proxyHopGroupRepository) Delete(ctx context.Context, id int64) error {
	err := r.client.ProxyHopGroup.DeleteOneID(id).Exec(ctx)
	if dbent.IsConstraintError(err) {
		return service.ErrProxyHopGroupInUse
	}
	return translatePersistenceError(err, service.ErrProxyHopGroupNotFound, nil)
}

func applyProxyHopGroupUpdate(builder *dbent.ProxyUpdateOne, id *int64) {
	if id == nil {
		builder.ClearUpstreamHopGroupID()
	} else {
		builder.SetUpstreamHopGroupID(*id)
	}
}
