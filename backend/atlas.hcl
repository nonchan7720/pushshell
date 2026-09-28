# Atlas (https://atlasgo.io) — versioned migration ファイル (migrations/<dialect>/)
# の適用・検証に使う。
#
# ent スキーマ (internal/ent/schema) から migration ファイルを "生成" する処理は
# ここでは行わない。このリポジトリで使える Atlas CLI はコミュニティビルドで、
# ent の公式ドキュメントが使う `src = "ent://..."` ローダーを含まないため
# (`ent:// scheme is not supported by the community version`)、生成は
# `go run ./cmd/migrate diff <name> --dialect <dialect>` (ent + Atlas を Go
# ライブラリとして呼ぶ、内製の migrate プログラム) で行う。
# 参考: https://entgo.io/docs/versioned-migrations
#
#   go run ./cmd/migrate diff <name> --dialect sqlite   # migration 生成
#   atlas migrate apply  --env sqlite --url "sqlite://data/app.db"
#   atlas migrate status --env sqlite --url "sqlite://data/app.db"
#   atlas migrate lint   --env sqlite --latest 1
#
# mysql / postgres の apply/status/lint も同様に --env mysql|postgres と、
# その方言の URL (ATLAS_URL, 下記コメント参照) を渡す。

env "sqlite" {
  # apply/status/lint の対象データベース。--url で上書きしない場合はこれを使う。
  url = getenv("ATLAS_URL")
  dev = "sqlite://file?mode=memory&_fk=1"
  migration {
    dir = "file://migrations/sqlite"
  }
}

env "mysql" {
  url = getenv("ATLAS_URL")
  # dev database (`atlas migrate diff`/`lint` の差分計算用) には Docker が要る。
  dev = "docker://mysql/8/dev"
  migration {
    dir = "file://migrations/mysql"
  }
}

env "postgres" {
  url = getenv("ATLAS_URL")
  # dev database (`atlas migrate diff`/`lint` の差分計算用) には Docker が要る。
  dev = "docker://postgres/16/dev?search_path=public"
  migration {
    dir = "file://migrations/postgres"
  }
}
