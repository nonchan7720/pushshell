package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// DeviceLogin は Device と Web アプリのログイン ID を紐付ける中間テーブル
// (多対多: 1 端末に複数アカウント、1 アカウントに複数端末)。
type DeviceLogin struct {
	ent.Schema
}

// Fields of the DeviceLogin.
func (DeviceLogin) Fields() []ent.Field {
	return []ent.Field{
		field.String("login_id").
			MaxLen(256).
			NotEmpty().
			Comment("Web アプリのログイン ID"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the DeviceLogin.
func (DeviceLogin) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("device", Device.Type).
			Ref("logins").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the DeviceLogin.
func (DeviceLogin) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("login_id").Edges("device").Unique(),
		index.Fields("login_id"),
	}
}
