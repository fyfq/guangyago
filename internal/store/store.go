package store

import (
	"database/sql"
	"errors"
	"fmt"
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
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录失败: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
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

func (s *Store) migrate() error {
	const schema = `
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
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("迁移失败: %w", err)
	}
	return nil
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

// GetByShareID 按分享 ID 读取。
func (s *Store) GetByShareID(shareID string) (*model.Link, error) {
	row := s.db.QueryRow(`SELECT `+columns+` FROM links WHERE share_id = ?`, shareID)
	return scanLink(row)
}

// List 按条件分页查询，返回数据与总数。
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
		where = append(where, "(title LIKE ? OR original_title LIKE ? OR raw_title LIKE ? OR url LIKE ? OR error LIKE ?)")
		kw := "%" + f.Keyword + "%"
		args = append(args, kw, kw, kw, kw, kw)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit, offset := f.Limit, f.Offset
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT ` + columns + ` FROM links WHERE ` + clause +
		` ORDER BY id DESC LIMIT ? OFFSET ?`
	rows, err := s.db.Query(q, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	links := make([]model.Link, 0, limit)
	for rows.Next() {
		l, err := scanRows(rows)
		if err != nil {
			return nil, 0, err
		}
		links = append(links, *l)
	}
	return links, total, rows.Err()
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
title = ?, original_title = ?, year = ?, media_type = ?, tmdb_id = ?,
overview = ?, genres = ?, vote_average = ?, runtime = ?, poster_url = ?, poster_path = ?,
status = ?, error = '', updated_at = CURRENT_TIMESTAMP
WHERE id = ?`,
		l.Title, l.OriginalTitle, l.Year, string(l.MediaType), l.TMDBID,
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

// PendingForScrape 取出待刮削的链接，供 M2 的 worker 使用。
func (s *Store) PendingForScrape(limit int) ([]model.Link, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+columns+` FROM links
WHERE status IN ('pending','failed','unmatched') ORDER BY id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Link{}
	for rows.Next() {
		l, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

const columns = `id, share_id, url, code, source, raw_title, title, original_title, year,
media_type, tmdb_id, overview, genres, vote_average, runtime, poster_url, poster_path,
shared_at, share_status, status, error, tg_message_id, created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanLink(row *sql.Row) (*model.Link, error) {
	l := &model.Link{}
	var createdAt, updatedAt string
	err := row.Scan(
		&l.ID, &l.ShareID, &l.URL, &l.Code, &l.Source, &l.RawTitle, &l.Title,
		&l.OriginalTitle, &l.Year, &l.MediaType, &l.TMDBID, &l.Overview, &l.Genres,
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

func scanRows(rows *sql.Rows) (*model.Link, error) {
	l := &model.Link{}
	var createdAt, updatedAt string
	err := rows.Scan(
		&l.ID, &l.ShareID, &l.URL, &l.Code, &l.Source, &l.RawTitle, &l.Title,
		&l.OriginalTitle, &l.Year, &l.MediaType, &l.TMDBID, &l.Overview, &l.Genres,
		&l.VoteAverage, &l.Runtime, &l.PosterURL, &l.PosterPath, &l.SharedAt,
		&l.ShareStatus, &l.Status, &l.Error, &l.TGMessageID, &createdAt, &updatedAt,
	)
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
