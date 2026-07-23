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

// BundlePlanGroup configures a group included in a bundle and optional
// per-platform (group platform) limits. Nil limits mean no per-platform cap.
type BundlePlanGroup struct{ ent.Schema }

func (BundlePlanGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "bundle_plan_groups"}}
}

func (BundlePlanGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("bundle_plan_id"),
		field.Int64("group_id"),
		field.Float("daily_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("monthly_limit_usd").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
	}
}

func (BundlePlanGroup) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("plan", BundlePlan.Type).Ref("groups").Field("bundle_plan_id").Unique().Required(),
		edge.From("group", Group.Type).Ref("bundle_plan_groups").Field("group_id").Unique().Required(),
	}
}

func (BundlePlanGroup) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bundle_plan_id", "group_id").Unique(), index.Fields("group_id")}
}
