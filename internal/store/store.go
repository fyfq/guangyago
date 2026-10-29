package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/odin/guangya-go/internal/model"
	_ "modernc.org/sqlite"
)

// Store 封装 SQLite 连接与链接表的全部读写操作。
type Store struct {
	db *sql.DB
}

// Open 打开数据库并执行迁移。
func Open(dsn string) (*Store, error) {
	if dir := filepath.Dir(dsn); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("创建数据目录失败: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("设置 %s 失败: %w", pragma, err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// schemaVersion 是当前代码期望的数据库结构版本，仅用于诊断；
// 迁移语句本身是幂等的（IF NOT EXISTS / INSERT OR IGNORE / 先查后加列）。
// v2：links 增加 release_date（TMDB 上映/首播日期），供门户按上映时间排序。
const schemaVersion = 2

func (s *Store) migrate() error {
	if _, err := s.db.Exec(migrateSQL); err != nil {
		return fmt.Errorf("迁移失败: %w", err)
	}
	if err := s.ensureReleaseDateColumn(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO schema_meta(key, value) VALUES('version', ?)`, fmt.Sprint(schemaVersion)); err != nil {
		return fmt.Errorf("写入 schema 版本失败: %w", err)
	}
	return nil
}

// ensureReleaseDateColumn 给存量库补上 release_date。
// SQLite 的 ALTER TABLE ADD COLUMN 不幂等，重复执行会报 duplicate column，
// 而 CREATE TABLE IF NOT EXISTS 对已经存在的表不加新列，所以先查表结构再决定要不要加。
func (s *Store) ensureReleaseDateColumn() error {
	var found int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('links') WHERE name = 'release_date'`).Scan(&found); err != nil {
		return fmt.Errorf("检查 links 表结构失败: %w", err)
	}
	if found > 0 {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE links ADD COLUMN release_date TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("添加 release_date 列失败: %w", err)
	}
	slog.Info("schema 迁移：links 已补 release_date 列", "schema_version", schemaVersion)
	return nil
}

const migrateSQL = `
CREATE TABLE IF NOT EXISTS links (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	share_id       TEXT NOT NULL UNIQUE,
	url            TEXT NOT NULL,
	code           TEXT NOT NULL DEFAULT '',
	source         TEXT NOT NULL DEFAULT 'guangya',
	raw_title      TEXT NOT NULL DEFAULT '',
	title          TEXT NOT NULL DEFAULT '',
	original_title TEXT NOT NULL DEFAULT '',
	year           INTEGER NOT NULL DEFAULT 0,
	release_date   TEXT NOT NULL DEFAULT '',
	media_type     TEXT NOT NULL DEFAULT '',
	tmdb_id        INTEGER NOT NULL DEFAULT 0,
	overview       TEXT NOT NULL DEFAULT '',
	genres         TEXT NOT NULL DEFAULT '',
	vote_average   REAL NOT NULL DEFAULT 0,
	runtime        INTEGER NOT NULL DEFAULT 0,
	poster_url     TEXT NOT NULL DEFAULT '',
	poster_path    TEXT NOT NULL DEFAULT '',
	shared_at      TEXT NOT NULL DEFAULT '',
	share_status   TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT 'pending',
	error          TEXT NOT NULL DEFAULT '',
	tg_message_id  INTEGER NOT NULL DEFAULT 0,
	created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_links_status ON links(status);
CREATE INDEX IF NOT EXISTS idx_links_tmdb_id ON links(tmdb_id);
CREATE INDEX IF NOT EXISTS idx_links_created_at ON links(created_at DESC);

CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	username      TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	display_name  TEXT NOT NULL DEFAULT '',
	role          TEXT NOT NULL DEFAULT 'operator',
	enabled       INTEGER NOT NULL DEFAULT 1,
	last_login_at DATETIME,
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS login_attempts (
	key       TEXT PRIMARY KEY,
	fails     INTEGER NOT NULL DEFAULT 0,
	until_ts  INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_login_attempts_until ON login_attempts(until_ts);

CREATE TABLE IF NOT EXISTS schema_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// likeEscape 把用户输入里的 LIKE 通配符转义，防止搜索关键词以 % 或 _ 操控匹配范围。
// 需搭配 SQL 里的 ESCAPE '\' 子句使用。
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Create 插入一条链接，share_id 重复时跳过。返回是否真正插入。
func (s *Store) Create(l *model.Link) (bool, error) {
	const q = `INSERT INTO links
(share_id, url, code, source, raw_title, title, year, tmdb_id, shared_at, share_status, status)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(share_id) DO NOTHING`
	res, err := s.db.Exec(q,
		l.ShareID, l.URL, l.Code, l.Source, l.RawTitle, l.Title,
		l.Year, l.TMDBID, l.SharedAt, l.ShareStatus, l.Status,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	id, err := res.LastInsertId()
	if err == nil {
		l.ID = id
	}
	return true, nil
}

// Get 按主键读取。
func (s *Store) Get(id int64) (*model.Link, error) {
	row := s.db.QueryRow(`SELECT `+columns+` FROM links WHERE id = ?`, id)
	return scanLink(row)
}

// List 按条件分页查询，返回数据与总数。
// limit 只处理 <=0 的默认值，上限由调用方自己收敛：HTTP 列表接口夹到 200，
// 导出与批量任务则需要一次拿到全部匹配记录。
func (s *Store) List(f model.ListFilter) ([]model.Link, int, error) {
	where := []string{"1=1"}
	args := []any{}
	// 多状态按 OR 匹配，单状态直接相等
	if len(f.Statuses) > 0 {
		ph := make([]string, 0, len(f.Statuses))
		for _, st := range f.Statuses {
			ph = append(ph, "?")
			args = append(args, string(st))
		}
		where = append(where, "status IN ("+strings.Join(ph, ",")+")")
	} else if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Keyword != "" {
		where = append(where, `(title LIKE ? ESCAPE '\' OR original_title LIKE ? ESCAPE '\' OR raw_title LIKE ? ESCAPE '\' OR url LIKE ? ESCAPE '\' OR error LIKE ? ESCAPE '\')`)
		kw := "%" + likeEscape(f.Keyword) + "%"
		args = append(args, kw, kw, kw, kw, kw)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit, offset := f.Limit, f.Offset
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	// clause 完全由本函数内的字面量片段拼出，用户输入只通过 args 占位符传递。
	q := `SELECT ` + columns + ` FROM links WHERE ` + clause + // #nosec G202 -- 拼接片段仅含常量与 ? 占位符
		` ORDER BY id DESC LIMIT ? OFFSET ?`
	rows, err := s.db.Query(q, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	links := make([]model.Link, 0, limit)
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, 0, err
		}
		links = append(links, *l)
	}
	return links, total, rows.Err()
}

// idsPlaceholders 为 IN 查询生成占位符与参数。
func idsPlaceholders[T any](values []T) (string, []any) {
	ph := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, v := range values {
		ph = append(ph, "?")
		args = append(args, v)
	}
	return strings.Join(ph, ","), args
}

// IDsByStatuses 只取 ID 供批量任务使用：状态集合可能覆盖上千条，
// 没必要把整行数据都读进内存。
func (s *Store) IDsByStatuses(statuses []model.Status) ([]int64, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	ph, args := idsPlaceholders(statuses)
	rows, err := s.db.Query(`SELECT id FROM links WHERE status IN (`+ph+`) ORDER BY id`, args...) // #nosec G202 -- ph 只由 idsPlaceholders 生成的 ? 组成
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, 256)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CountByStatus 一次 GROUP BY 取回各状态数量。
func (s *Store) CountByStatus() (map[model.Status]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM links GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[model.Status]int{}
	for rows.Next() {
		var (
			st string
			n  int
		)
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		counts[model.Status(st)] = n
	}
	return counts, rows.Err()
}

// UpdateStatus 更新状态与错误信息。
func (s *Store) UpdateStatus(id int64, status model.Status, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE links SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(status), errMsg, id)
	return err
}

// UpdateScraped 写入 TMDB 刮削结果。
func (s *Store) UpdateScraped(id int64, l *model.Link) error {
	_, err := s.db.Exec(`UPDATE links SET
title = ?, original_title = ?, year = ?, release_date = ?, media_type = ?, tmdb_id = ?,
overview = ?, genres = ?, vote_average = ?, runtime = ?, poster_url = ?, poster_path = ?,
status = ?, error = '', updated_at = CURRENT_TIMESTAMP
WHERE id = ?`,
		l.Title, l.OriginalTitle, l.Year, l.ReleaseDate, string(l.MediaType), l.TMDBID,
		l.Overview, l.Genres, l.VoteAverage, l.Runtime, l.PosterURL, l.PosterPath,
		string(model.StatusScraped), id)
	return err
}

// UpdatePushed 回写推送结果。
func (s *Store) UpdatePushed(id int64, messageID int64) error {
	_, err := s.db.Exec(`UPDATE links SET tg_message_id = ?, status = ?, error = '',
updated_at = CURRENT_TIMESTAMP WHERE id = ?`, messageID, string(model.StatusPushed), id)
	return err
}

// SetTMDBID 手动指定 TMDB ID 与类型，并把状态重置为待刮削。
func (s *Store) SetTMDBID(id, tmdbID int64, mediaType model.MediaType) error {
	_, err := s.db.Exec(
		`UPDATE links SET tmdb_id = ?, media_type = ?, status = ?, error = '',
updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		tmdbID, string(mediaType), string(model.StatusPending), id)
	return err
}

// Delete 删除一条记录。
func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM links WHERE id = ?`, id)
	return err
}

// DeleteMany 用一条语句批量删除，返回实际删除的行数。
func (s *Store) DeleteMany(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph, args := idsPlaceholders(ids)
	res, err := s.db.Exec(`DELETE FROM links WHERE id IN (`+ph+`)`, args...) // #nosec G202 -- ph 只由 idsPlaceholders 生成的 ? 组成
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const columns = `id, share_id, url, code, source, raw_title, title, original_title, year,
release_date, media_type, tmdb_id, overview, genres, vote_average, runtime, poster_url, poster_path,
shared_at, share_status, status, error, tg_message_id, created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanLink(row scanner) (*model.Link, error) {
	l := &model.Link{}
	var createdAt, updatedAt string
	err := row.Scan(
		&l.ID, &l.ShareID, &l.URL, &l.Code, &l.Source, &l.RawTitle, &l.Title,
		&l.OriginalTitle, &l.Year, &l.ReleaseDate, &l.MediaType, &l.TMDBID, &l.Overview, &l.Genres,
		&l.VoteAverage, &l.Runtime, &l.PosterURL, &l.PosterPath, &l.SharedAt,
		&l.ShareStatus, &l.Status, &l.Error, &l.TGMessageID, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l.CreatedAt = parseTime(createdAt)
	l.UpdatedAt = parseTime(updatedAt)
	return l, nil
}

func parseTime(v string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}
