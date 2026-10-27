//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/suite"
)

// 代理池（fork）：迁移 248 的触发器、绑定计数、锁定与默认池语义。
type ProxyPoolSuite struct {
	suite.Suite
	ctx   context.Context
	tx    *dbent.Tx
	proxy *proxyRepository
	pools *proxyPoolRepository
}

func (s *ProxyPoolSuite) SetupTest() {
	s.ctx = context.Background()
	s.tx = testEntTx(s.T())
	s.proxy = newProxyRepositoryWithSQL(s.tx.Client(), s.tx)
	s.pools = &proxyPoolRepository{client: s.tx.Client(), sql: s.tx}
}

func TestProxyPoolSuite(t *testing.T) { suite.Run(t, new(ProxyPoolSuite)) }

func (s *ProxyPoolSuite) mkProxy(name string, poolID *int64) int64 {
	return s.mkProxyHost(name, "pool-test-"+name+".invalid", poolID)
}

// mkProxyHost 指定落地 IP（host）：计数按 host 合并，测试默认给每个代理不同的 host。
func (s *ProxyPoolSuite) mkProxyHost(name, host string, poolID *int64) int64 {
	p := &service.Proxy{Name: name, Protocol: "http", Host: host, Port: 8080,
		Status: service.StatusActive, FallbackMode: service.FallbackModeNone, ExpiryWarnDays: 7, PoolID: poolID}
	s.Require().NoError(s.proxy.Create(s.ctx, p))
	return p.ID
}

func (s *ProxyPoolSuite) mkAccount(platform string, proxyID *int64) int64 {
	var id int64
	s.Require().NoError(scanSingleRow(s.ctx, s.tx, `
		INSERT INTO accounts (name, platform, type, credentials, extra, status, proxy_id, created_at, updated_at)
		VALUES ($1,$2,'oauth','{}','{}','active',$3,NOW(),NOW()) RETURNING id`,
		[]any{"acc-" + time.Now().Format("150405.000000000"), platform, proxyID}, &id))
	return id
}

func (s *ProxyPoolSuite) exec(q string, args ...any) {
	_, err := s.tx.ExecContext(s.ctx, q, args...)
	s.Require().NoError(err)
}

func (s *ProxyPoolSuite) defaultPool() *service.ProxyPool {
	pools, err := s.pools.ListPools(s.ctx)
	s.Require().NoError(err)
	for i := range pools {
		if pools[i].IsDefault {
			return &pools[i]
		}
	}
	s.FailNow("default pool missing")
	return nil
}

func (s *ProxyPoolSuite) member(pool *service.ProxyPool, proxyID int64) service.ProxyPoolMember {
	members, err := s.pools.ListMembers(s.ctx, pool, 0)
	s.Require().NoError(err)
	for _, m := range members {
		if m.Proxy.ID == proxyID {
			return m
		}
	}
	s.FailNow("proxy not in pool")
	return service.ProxyPoolMember{}
}

func (s *ProxyPoolSuite) TestDefaultPoolSeededAndUndeletable() {
	def := s.defaultPool()
	s.Require().Equal("default", def.Name)
	s.Require().ErrorIs(s.pools.DeletePool(s.ctx, def.ID), service.ErrProxyPoolNotFound)
}

func (s *ProxyPoolSuite) TestTriggerRecordsHistoryAcrossChangesAndDelete() {
	def := s.defaultPool()
	p1 := s.mkProxy("p1", nil)
	p2 := s.mkProxy("p2", nil)

	a1 := s.mkAccount("kiro", &p1)
	a2 := s.mkAccount("kiro", &p1)
	a3 := s.mkAccount("openai", &p1)

	m := s.member(def, p1)
	s.Require().Equal(int64(2), m.HistoryCounts["kiro"])
	s.Require().Equal(int64(2), m.ActiveCounts["kiro"])
	s.Require().Equal(int64(1), m.HistoryCounts["openai"])

	// 改代理：p1 的历史计数不变、在用数减少；p2 新增一条。
	s.exec(`UPDATE accounts SET proxy_id=$1 WHERE id=$2`, p2, a1)
	// 删除账号：历史仍计数。
	s.exec(`UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, a2)
	// 无关列更新不产生新记录。
	s.exec(`UPDATE accounts SET name='renamed' WHERE id=$1`, a3)

	m = s.member(def, p1)
	s.Require().Equal(int64(2), m.HistoryCounts["kiro"])
	s.Require().Equal(int64(0), m.ActiveCounts["kiro"])
	s.Require().Equal(int64(1), s.member(def, p2).HistoryCounts["kiro"])

	// 同一账号切回 p1 不重复计数（按不同账号去重）。
	s.exec(`UPDATE accounts SET proxy_id=$1 WHERE id=$2`, p1, a1)
	m = s.member(def, p1)
	s.Require().Equal(int64(2), m.HistoryCounts["kiro"])
	s.Require().Equal(int64(1), m.ActiveCounts["kiro"])

	bindings, err := s.pools.ListBindingsByProxy(s.ctx, p1)
	s.Require().NoError(err)
	reasons := map[string]int{}
	for _, b := range bindings {
		reasons[b.UnbindReason]++
	}
	s.Require().Equal(1, reasons["proxy_changed"])
	s.Require().Equal(1, reasons["account_deleted"])

	// excludeAccountID 不计入该账号。
	members, err := s.pools.ListMembers(s.ctx, def, a1)
	s.Require().NoError(err)
	for _, mm := range members {
		if mm.Proxy.ID == p1 {
			s.Require().Equal(int64(1), mm.HistoryCounts["kiro"])
		}
	}
}

func (s *ProxyPoolSuite) TestLockSurvivesSystemReassignAndUnlock() {
	pool := &service.ProxyPool{Name: "kiro-pool"}
	s.Require().NoError(s.pools.CreatePool(s.ctx, pool))
	p1 := s.mkProxy("lp1", &pool.ID)
	p2 := s.mkProxy("lp2", &pool.ID)
	a := s.mkAccount("kiro", &p1)

	s.Require().NoError(s.pools.LockBinding(s.ctx, a, pool.ID))
	locked, err := s.pools.ListLockedAccountIDs(s.ctx, []int64{a})
	s.Require().NoError(err)
	s.Require().Equal([]int64{a}, locked)

	// 系统改投（如代理过期回退）沿用锁定与来源池。
	s.exec(`UPDATE accounts SET proxy_id=$1 WHERE id=$2`, p2, a)
	b, err := s.pools.GetOpenBinding(s.ctx, a)
	s.Require().NoError(err)
	s.Require().Equal(p2, b.ProxyID)
	s.Require().True(b.Locked)
	s.Require().Equal(service.ProxyBindingSourcePool, b.Source)
	s.Require().Equal(pool.ID, *b.PoolID)
	s.Require().Equal("kiro-pool", b.PoolName)

	s.Require().NoError(s.pools.UnlockBinding(s.ctx, a))
	locked, err = s.pools.ListLockedAccountIDs(s.ctx, []int64{a})
	s.Require().NoError(err)
	s.Require().Empty(locked)
	s.Require().ErrorIs(s.pools.UnlockBinding(s.ctx, a), service.ErrProxyPoolBindingNotFound)
}

func (s *ProxyPoolSuite) TestShadowAccountsNotRecorded() {
	def := s.defaultPool()
	p := s.mkProxy("sp", nil)
	parent := s.mkAccount("openai", &p)
	var shadow int64
	s.Require().NoError(scanSingleRow(s.ctx, s.tx, `
		INSERT INTO accounts (name, platform, type, credentials, extra, status, proxy_id, parent_account_id, quota_dimension, created_at, updated_at)
		VALUES ('shadow','openai','oauth','{}','{}','active',$1,$2,'spark',NOW(),NOW()) RETURNING id`,
		[]any{p, parent}, &shadow))
	s.Require().Equal(int64(1), s.member(def, p).HistoryCounts["openai"])
}

func (s *ProxyPoolSuite) TestAssignAndDeletePoolMovesProxiesToDefault() {
	def := s.defaultPool()
	pool := &service.ProxyPool{Name: "tmp-pool"}
	s.Require().NoError(s.pools.CreatePool(s.ctx, pool))
	p := s.mkProxy("mv", nil)

	s.Require().NoError(s.pools.AssignProxies(s.ctx, pool, []int64{p}))
	got, err := s.proxy.GetByID(s.ctx, p)
	s.Require().NoError(err)
	s.Require().Equal(pool.ID, *got.PoolID)

	s.Require().NoError(s.pools.DeletePool(s.ctx, pool.ID))
	got, err = s.proxy.GetByID(s.ctx, p)
	s.Require().NoError(err)
	s.Require().Nil(got.PoolID)
	s.member(def, p) // 默认池包含 pool_id 为 NULL 的代理

	// 重名放最后：唯一约束错误会让 Postgres 事务进入 aborted 状态。
	s.Require().ErrorIs(s.pools.CreatePool(s.ctx, &service.ProxyPool{Name: "default"}), service.ErrProxyPoolNameExists)
}

func (s *ProxyPoolSuite) TestHistoryMergedBySameLandingHost() {
	def := s.defaultPool()
	p1 := s.mkProxyHost("h1", "203.0.113.7", nil)
	_ = s.mkAccount("kiro", &p1)
	// 删除后用同一落地 IP 重新登记：历史计数延续，绕不过平台上限。
	s.exec(`UPDATE accounts SET deleted_at=NOW() WHERE proxy_id=$1`, p1)
	s.exec(`UPDATE proxies SET deleted_at=NOW() WHERE id=$1`, p1)
	p2 := s.mkProxyHost("h2", "203.0.113.7", nil)
	_ = s.mkAccount("kiro", &p2)
	m := s.member(def, p2)
	s.Require().Equal(int64(2), m.HistoryCounts["kiro"])
	s.Require().Equal(int64(1), m.ActiveCounts["kiro"])
}

func (s *ProxyPoolSuite) TestPolicyCRUD() {
	pool := &service.ProxyPool{Name: "policy-pool"}
	s.Require().NoError(s.pools.CreatePool(s.ctx, pool))
	lifetime := 5
	p := &service.ProxyIPPolicy{Platform: "test-churn", Mode: service.ProxyIPPolicyModeChurn, PoolIDs: []int64{pool.ID},
		MaxLifetimeAccounts: &lifetime, OnEgressFailure: service.ProxyIPFailureReassign}
	s.Require().NoError(s.pools.UpsertPolicy(s.ctx, p))
	s.Require().NotZero(p.ID)

	one := 1
	p.Mode, p.MaxLifetimeAccounts, p.MaxActiveAccounts, p.PoolIDs = service.ProxyIPPolicyModeSticky, nil, &one, nil
	s.Require().NoError(s.pools.UpsertPolicy(s.ctx, p))

	policies, err := s.pools.ListPolicies(s.ctx)
	s.Require().NoError(err)
	var got *service.ProxyIPPolicy
	for i := range policies {
		if policies[i].Platform == "test-churn" {
			got = &policies[i]
		}
	}
	s.Require().NotNil(got)
	s.Require().Equal(service.ProxyIPPolicyModeSticky, got.Mode)
	s.Require().Nil(got.MaxLifetimeAccounts)
	s.Require().Equal(1, *got.MaxActiveAccounts)
	s.Require().Empty(got.PoolIDs)

	s.Require().NoError(s.pools.DeletePolicy(s.ctx, "test-churn"))
	s.Require().ErrorIs(s.pools.DeletePolicy(s.ctx, "test-churn"), service.ErrProxyIPPolicyNotFound)
}
