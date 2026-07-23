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

// BundlePlan is the product definition for a cross-platform subscription.
// It is deliberately separate from SubscriptionPlan so legacy products and
// contracts can continue to use their historical semantics.
type BundlePlan struct{ ent.Schema }

func (BundlePlan) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "bundle_plans"}}
}

func (BundlePlan) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(100).NotEmpty(),
		field.String("description").SchemaType(map[string]string{dialect.Postgres: "text"}).Default(""),
		field.String("product_name").MaxLen(100).Default(""),
		field.Float("price").SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}),
		field.Float("original_price").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}),
		field.String("currency").MaxLen(3).Default("USD"),
		field.Int("validity_days").Default(30),
		field.String("validity_unit").MaxLen(10).Default("day"),
		field.Float("shared_daily_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("shared_monthly_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.String("features").SchemaType(map[string]string{dialect.Postgres: "text"}).Default(""),
		field.Bool("for_sale").Default(true),
		field.Int("sort_order").Default(0),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (BundlePlan) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("groups", BundlePlanGroup.Type),
		edge.To("subscriptions", BundleSubscription.Type),
	}
}

func (BundlePlan) Indexes() []ent.Index {
	return []ent.Index{index.Fields("for_sale"), index.Fields("sort_order")}
}
