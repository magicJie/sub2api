package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Proxy holds the schema definition for the Proxy entity.
type Proxy struct {
	ent.Schema
}

func (Proxy) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "proxies"},
	}
}

func (Proxy) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (Proxy) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.String("protocol").
			MaxLen(20).
			NotEmpty(),
		field.String("host").
			MaxLen(255).
			NotEmpty(),
		field.Int("port"),
		field.String("username").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("password").
			MaxLen(100).
			Optional().
			Nillable(),
		field.String("status").
			MaxLen(20).
			Default("active"),
		field.Time("expires_at").
			Optional().Nillable().
			Comment("Proxy expiration time (NULL means never expires)."),
		field.String("fallback_mode").
			MaxLen(20).Default("none").
			Comment("Fallback target on expiry: none | proxy | direct."),
		field.Int64("backup_proxy_id").
			Optional().Nillable().
			Comment("Backup proxy id when fallback_mode=proxy (self-reference)."),
		field.Int("expiry_warn_days").
			Default(7).
			Comment("Days before expiry to flag as expiring-soon (per proxy)."),
		// 链式代理（fork）：前置代理与 Xray 协议参数，见 internal/pkg/proxychain。
		field.String("upstream_mode").
			MaxLen(20).Default("direct").
			Comment("Upstream (front) proxy mode: direct | default | proxy | hop_group."),
		field.Int64("upstream_proxy_id").
			Optional().Nillable().
			Comment("Upstream proxy id when upstream_mode=proxy (self-reference, app-level checked)."),
		field.Int64("upstream_hop_group_id").
			Optional().Nillable().
			Comment("链式代理（fork）：first-hop group id when upstream_mode=hop_group."),
		field.JSON("options", map[string]string{}).
			Optional().
			Comment("Protocol/transport options for ss/vmess/vless/trojan (cipher, network, security, sni ...)."),
		// 代理池（fork）：所属代理池分组，见 internal/service/proxy_pool.go。
		field.Int64("pool_id").
			Optional().Nillable().
			Comment("Proxy pool id (proxy_pools, app-level checked). NULL is treated as the default pool."),
	}
}

// Edges 定义代理实体的关联关系。
func (Proxy) Edges() []ent.Edge {
	return []ent.Edge{
		// accounts: 使用此代理的账户（反向边）
		edge.From("accounts", Account.Type).
			Ref("proxy"),
		// Directed many-to-one: a backup can serve multiple primary proxies.
		// The inverse edge prevents Ent from treating this self-reference as symmetric.
		edge.From("primary_proxies", Proxy.Type).
			Ref("backup_proxy"),
		edge.To("backup_proxy", Proxy.Type).
			Field("backup_proxy_id").
			Unique(),
	}
}

func (Proxy) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("deleted_at"),
		index.Fields("expires_at"),
		index.Fields("backup_proxy_id"),
		index.Fields("pool_id"),
	}
}
