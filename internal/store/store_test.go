package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/odin/guangya-go/internal/model"
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
	if v != "2" {
		t.Errorf("schema_meta.version 期望 '2'，实际 %q", v)
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
			id, seq            int
			table, from, to    string
			onUpdate, onDelete string
			match              string
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

// v1 存量库升级要能补上 release_date；CREATE TABLE IF NOT EXISTS 不会给已存在的表加列，
// 而 ALTER 不幂等，所以再开一次也不能报 duplicate column。
func TestMigrate_AddsReleaseDateToLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(migrateSQL); err != nil {
		t.Fatalf("建 v1 表失败: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO links(share_id, url, title, year, status)
		VALUES('s1', 'https://example.com/s/1', '老记录', 2024, 'scraped')`); err != nil {
		t.Fatalf("写入存量记录失败: %v", err)
	}
	if _, err := raw.Exec(`ALTER TABLE links DROP COLUMN release_date`); err != nil {
		t.Fatalf("模拟 v1 表结构失败: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("迁移 v1 库失败: %v", err)
	}
	var found int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('links') WHERE name = 'release_date'`).Scan(&found); err != nil {
		t.Fatalf("查表结构失败: %v", err)
	}
	if found != 1 {
		t.Error("迁移后 links 应有 release_date 列")
	}
	// 缺列时写入的存量行不能被迁移弄丢，且新列取到默认空串
	l, err := s.Get(1)
	if err != nil {
		t.Fatalf("读存量记录失败: %v", err)
	}
	if l == nil {
		t.Fatal("存量记录丢失")
	}
	if l.ReleaseDate != "" {
		t.Errorf("存量记录 release_date 应为空，实际 %q", l.ReleaseDate)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err != nil {
		t.Fatalf("重复迁移不应失败: %v", err)
	}
}

// 门户按上映时间排序：完整日期优先，只有年份的退到当年 1 月 1 日参与同一套顺序，
// 两者都没有的排最后。
func TestListPublic_SortByReleaseDate(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sort.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cases := []struct {
		title   string
		year    int
		release string
	}{
		{title: "只有2025年", year: 2025},
		{title: "2024-06-01", year: 2024, release: "2024-06-01"},
		{title: "2024-01-15", year: 2024, release: "2024-01-15"},
		{title: "只有2024年", year: 2024},
		{title: "既没日期也没年份", year: 0},
	}
	for _, c := range cases {
		l := &model.Link{
			ShareID: "share-" + c.title, URL: "https://example.com/s/" + c.title,
			Title: c.title, Year: c.year, Status: model.StatusScraped,
		}
		created, err := s.Create(l)
		if err != nil || !created {
			t.Fatalf("建记录 %s 失败: created=%v err=%v", c.title, created, err)
		}
		l.ReleaseDate = c.release
		if err := s.UpdateScraped(l.ID, l); err != nil {
			t.Fatalf("写入刮削结果 %s 失败: %v", c.title, err)
		}
	}

	items, _, err := s.ListPublic(model.PublicFilter{Sort: "release", Limit: 10})
	if err != nil {
		t.Fatalf("按上映时间查询失败: %v", err)
	}
	got := make([]string, 0, len(items))
	for _, it := range items {
		got = append(got, it.Title)
	}
	want := []string{"只有2025年", "2024-06-01", "2024-01-15", "只有2024年", "既没日期也没年份"}
	if len(got) != len(want) {
		t.Fatalf("返回 %d 条，期望 %d 条：%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 位期望 %q，实际 %q（全部：%v）", i+1, want[i], got[i], got)
		}
	}
}
