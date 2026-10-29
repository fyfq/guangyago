package store

import (
	"net/url"
	"strings"

	"github.com/odin/guangya-go/internal/model"
)

// 门户只展示已推送成功的记录，按分享时间倒序时把空值排到最后。
const publicWhere = `status = 'pushed'`

const publicColumns = `id, title, original_title, year, media_type, tmdb_id, overview, genres,
vote_average, runtime, poster_url, url, code, shared_at`

// publicOrder 是允许的排序方式白名单，避免把用户输入拼进 SQL。
var publicOrder = map[string]string{
	"new":    `CASE WHEN shared_at = '' THEN 1 ELSE 0 END, shared_at DESC, id DESC`,
	"rating": `vote_average DESC, id DESC`,
	"year":   `year DESC, id DESC`,
}

// ListPublic 查询门户列表，返回记录与总数。
func (s *Store) ListPublic(f model.PublicFilter) ([]model.PublicLink, int, error) {
	clause, args := publicWhereClause(f, true, true)

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order, ok := publicOrder[f.Sort]
	if !ok {
		order = publicOrder["new"]
	}
	limit, offset := f.Limit, f.Offset
	if limit <= 0 {
		limit = 24
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.Query(
		`SELECT `+publicColumns+` FROM links WHERE `+clause+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]model.PublicLink, 0, limit)
	for rows.Next() {
		var p model.PublicLink
		if err := rows.Scan(
			&p.ID, &p.Title, &p.OriginalTitle, &p.Year, &p.MediaType, &p.TMDBID,
			&p.Overview, &p.Genres, &p.VoteAverage, &p.Runtime, &p.PosterURL,
			&p.URL, &p.Code, &p.SharedAt,
		); err != nil {
			return nil, 0, err
		}
		p.URL = safeLinkURL(p.URL)
		items = append(items, p)
	}
	return items, total, rows.Err()
}

// publicWhereClause 拼接门户查询条件。分类数量和年份选项各自要在「其他条件已定」
// 的前提下统计，所以由调用方决定本次是否带上分类、年份。
func publicWhereClause(f model.PublicFilter, withMedia, withYear bool) (string, []any) {
	where := []string{publicWhere}
	args := []any{}

	if withMedia && f.MediaType != "" {
		where = append(where, "media_type = ?")
		args = append(args, string(f.MediaType))
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		where = append(where, "(title LIKE ? OR original_title LIKE ? OR genres LIKE ?)")
		like := "%" + kw + "%"
		args = append(args, like, like, like)
	}
	if withYear && f.Year > 0 {
		where = append(where, "year = ?")
		args = append(args, f.Year)
	}
	return strings.Join(where, " AND "), args
}

// safeLinkURL 只放行 http/https 链接，避免历史脏数据把 javascript: 之类的地址带到前台。
func safeLinkURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return raw
}

// PublicStats 返回门户筛选项的计数：分类数字忽略分类本身、年份选项忽略年份本身，
// 这样每一组选项都反映「切换过去会得到多少条」。
func (s *Store) PublicStats(f model.PublicFilter) (model.PublicStats, error) {
	var out model.PublicStats

	clause, args := publicWhereClause(f, false, true)
	if err := s.db.QueryRow(
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN media_type = 'movie' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN media_type = 'tv' THEN 1 ELSE 0 END), 0)
		 FROM links WHERE `+clause, args...).Scan(&out.Total, &out.Movies, &out.TV); err != nil {
		return out, err
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM links WHERE ` + publicWhere).Scan(&out.LibraryTotal); err != nil {
		return out, err
	}

	clause, args = publicWhereClause(f, true, false)
	rows, err := s.db.Query(
		`SELECT year, COUNT(*) FROM links WHERE `+clause+` AND year > 0 GROUP BY year ORDER BY year DESC`,
		args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	out.Years = make([]model.PublicYearStat, 0, 32)
	for rows.Next() {
		var y model.PublicYearStat
		if err := rows.Scan(&y.Year, &y.Count); err != nil {
			return out, err
		}
		out.Years = append(out.Years, y)
	}
	return out, rows.Err()
}
