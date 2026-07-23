package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BundleSubscriptionEntitlement is an immutable fulfillment snapshot. It
// prevents later plan/group edits from changing an already purchased contract.
type BundleSubscriptionEntitlement struct{ ent.Schema }

func (BundleSubscriptionEntitlement) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "bundle_subscription_entitlements"}}
}

func (BundleSubscriptionEntitlement) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("bundle_subscription_id"),
		field.Int64("group_id"),
		field.String("platform").MaxLen(50),
		field.Float("daily_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("monthly_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("daily_usage_usd").Default(0).SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
		field.Float("monthly_usage_usd").Default(0).SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}),
	}
}

func (BundleSubscriptionEntitlement) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("subscription", BundleSubscription.Type).Ref("entitlements").Field("bundle_subscription_id").Unique().Required(),
		edge.From("group", Group.Type).Ref("bundle_subscription_entitlements").Field("group_id").Unique().Required(),
	}
}

func (BundleSubscriptionEntitlement) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bundle_subscription_id", "group_id").Unique(), index.Fields("group_id", "platform")}
}
