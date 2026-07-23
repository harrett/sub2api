package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BundleSubscription is a user contract. At most one active and one pending
// contract are enforced by partial indexes in the migration.
type BundleSubscription struct{ ent.Schema }

func (BundleSubscription) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "bundle_subscriptions"}}
}

func (BundleSubscription) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.Int64("bundle_plan_id"),
		field.String("status").MaxLen(20).Default("pending"),
		field.Time("starts_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("expires_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("daily_window_start").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("monthly_window_start").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Float("daily_usage_usd").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		field.Float("monthly_usage_usd").SchemaType(map[string]string{dialect.Postgres: "decimal(20,10)"}).Default(0),
		field.Int64("payment_order_id").Optional().Nillable(),
		field.Int64("assigned_by").Optional().Nillable(),
		field.Time("assigned_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("notes").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (BundleSubscription) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("bundle_subscriptions").Field("user_id").Unique().Required(),
		edge.From("plan", BundlePlan.Type).Ref("subscriptions").Field("bundle_plan_id").Unique().Required(),
		edge.To("entitlements", BundleSubscriptionEntitlement.Type),
	}
}

func (BundleSubscription) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id"), index.Fields("bundle_plan_id"), index.Fields("status"), index.Fields("expires_at"), index.Fields("user_id", "status")}
}
