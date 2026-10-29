package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLikeEscape(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"50%", `50\%`},
		{"a_b", `a\_b`},
		{`a\b`, `a\\b`},
		{`%_\`, `\%\_\\`},
		{`100% pure_python\code`, `100\% pure\_python\\code`},
	}
	for _, c := range cases {
		if got := likeEscape(c.in); got != c.want {
			t.Errorf("likeEscape(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestParseTime(t *testing.T) {
	cases := []struct {
		in    string
		isNil bool
	}{
		{"", true},
		{"not-a-time", true},
		{"2026-10-01T12:34:56Z", false}, // RFC3339
		{"2026-10-01 12:34:56", false},  // SQLite 默认
	}
	for _, c := range cases {
		got := parseTime(c.in)
		if c.isNil {
			if !got.IsZero() {
				t.Errorf("parseTime(%q) 应为零值，实际 %v", c.in, got)
			}
			continue
		}
		if got.IsZero() {
			t.Errorf("parseTime(%q) 解析失败", c.in)
		}
	}
}

// Open 应该建目录 + 应用 PRAGMA + 建表。
func TestOpen_CreatesDirAndTables(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "sub", "db.sqlite")
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(dsn); err != nil {
		t.Errorf("数据库文件未创建: %v", err)
	}

	// 建表：直接查一次 schema_meta 的 version
	var v string
	if err := s.db.QueryRow(`SELECT value FROM schema_meta WHERE key='version'`).Scan(&v); err != nil {
		t.Fatalf("读 schema_meta.version 失败: %v", err)
	}
	if v != "1" {
		t.Errorf("schema_meta.version 期望 '1'，实际 %q", v)
	}

	// PRAGMA foreign_keys 应开启
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("读 PRAGMA foreign_keys 失败: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys 应为 1，实际 %d", fk)
	}

	// sessions 表的 user_id 应带 FK 指向 users
	fks, err := s.db.Query(`PRAGMA foreign_key_list(sessions)`)
	if err != nil {
		t.Fatalf("读 FK 列表失败: %v", err)
	}
	defer fks.Close()
	var found bool
	for fks.Next() {
		var (
			id, seq               int
			table, from, to       string
			onUpdate, onDelete    string
			match                 string
		)
		if err := fks.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("扫描 FK: %v", err)
		}
		if table == "users" && onDelete == "CASCADE" {
			found = true
		}
	}
	if !found {
		t.Error("sessions.user_id 未级联到 users(id) ON DELETE CASCADE")
	}
}

// 关闭后重开不应丢数据，验证 WAL 与迁移幂等。
func TestOpen_ReopenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO users(username, password_hash, role, enabled) VALUES('u1','h','admin',1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("重开失败: %v", err)
	}
	defer s2.Close()
	var n int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username='u1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("重开后应保留 1 条用户，实际 %d", n)
	}
}
