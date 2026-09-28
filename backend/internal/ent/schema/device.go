package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Device はプッシュ通知の宛先となる端末 (アプリのインストール単位)。
// installation_id をキーに upsert され、login_id によって通知の宛先を引く。
type Device struct {
	ent.Schema
}

// Fields of the Device.
func (Device) Fields() []ent.Field {
	return []ent.Field{
		field.String("installation_id").
			MaxLen(128).
			NotEmpty().
			Unique().
			Comment("アプリインストールごとに生成される安定した ID"),
		field.String("login_id").
			MaxLen(256).
			NotEmpty().
			Comment("Web アプリのログイン ID"),
		field.Enum("platform").
			Values("ios", "android"),
		field.String("push_token").
			MaxLen(512).
			NotEmpty().
			Optional().
			Comment("Expo Push Token"),
		field.String("device_token").
			MaxLen(4096).
			Optional().
			Comment("ネイティブのデバイストークン (APNs / FCM)"),
		field.String("app_id").MaxLen(256).Optional(),
		field.String("app_version").MaxLen(64).Optional(),
		field.String("build_number").MaxLen(64).Optional(),
		field.String("os_version").MaxLen(64).Optional(),
		field.String("device_model").MaxLen(128).Optional(),
		field.String("locale").MaxLen(32).Optional(),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Device.
func (Device) Edges() []ent.Edge {
	return nil
}

// Indexes of the Device.
func (Device) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("login_id"),
		index.Fields("push_token"),
	}
}
