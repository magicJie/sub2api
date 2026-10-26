package schema

// 链式代理（fork）：mihomo 自动切换首跳组，迁移 249。

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

type ProxyHopGroup struct{ ent.Schema }

func (ProxyHopGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "proxy_hop_groups"}}
}
func (ProxyHopGroup) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }
func (ProxyHopGroup) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(255).NotEmpty().Unique(),
		field.String("strategy").MaxLen(20).Default("fallback"),
		field.String("test_url").Default("https://www.gstatic.com/generate_204"),
		field.Int("interval_seconds").Default(60).Positive(),
		field.Int("tolerance_ms").Default(50).NonNegative(),
		field.JSON("subscription_urls", []string{}).Default([]string{}).Sensitive(),
		field.JSON("member_proxy_ids", []int64{}).Default([]int64{}),
		field.Int("subscription_update_interval").Default(3600).Positive(),
		field.String("status").MaxLen(20).Default("active"),
	}
}
