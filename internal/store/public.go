package store

import (
	"net/url"
	"strings"

	"github.com/odin/guangya-go/internal/model"
)

// 刮削成功即视为收录，推送只是发布到频道的动作，不影响门户可见性。
// 集合与 model.ScrapedOnly 保持一致，新增状态时两处同步。
const publicWhere = `status IN ('scraped', 'pushed', 'push_failed')`

// defaultSitemapLimit 是调用方没给上限时单次返回的 sitemap 条目数。
const defaultSitemapLimit = 5000

const publicColumns = `id, title, original_title, year, media_type, tmdb_id, overview, genres,
vote_average, runtime, poster_url, url, code, shared_at, release_date`

// publicGroupExpr 把同一个 TMDB 条目折叠成门户里的一条资源：同一部片允许多条分享链接。
// TMDB ID 只在同类型内唯一，所以分组键必须带上 media_type；未刮削出 TMDB ID 的记录按自身 ID 独立成组。
const publicGroupExpr = `CASE WHEN tmdb_id > 0 THEN media_type || ':' || tmdb_id ELSE 'id:' || id END`

// publicOrder 是允许的排序方式白名单，避免把用户输入拼进 SQL。
var publicOrder = map[string]string{
	"new": `CASE WHEN shared_at = '' THEN 1 ELSE 0 END, shared_at DESC, id DESC`,
	// 上映时间：存量记录多半只有年份，缺完整日期时退到当年 1 月 1 日参与同一套字典序，
	// 既不会把老数据整体甩到末尾，也能和新刮出的精确日期正确穿插。
	"release": `COALESCE(NULLIF(release_date, ''), CASE WHEN year > 0 THEN year || '-01-01' ELSE '' END) DESC, id DESC`,
	"rating":  `vote_average DESC, id DESC`,
	"year":    `year DESC, id DESC`,
}

// ListPublic 查询门户列表，返回分组后的资源与分组总数。
// 代表行取组内最近分享的一条，排序也按代表行的字段进行。
func (s *Store) ListPublic(f model.PublicFilter) ([]model.PublicLink, int, error) {
	filter, args := publicFilterClause(f, true, true)

	var total int
	if err := s.db.QueryRow(
		`SELECT COUNT(DISTINCT `+publicGroupExpr+`) FROM links WHERE `+publicWhere+filter, // #nosec G202 -- 表达式为本文件常量，值走占位符
		args...).Scan(&total); err != nil {
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
		// pub/filter 子句、order 均来自本文件常量与 publicOrder 白名单，用户输入只通过 ? 占位符传入。
		`WITH pub AS (
			SELECT id, title, original_title, year, media_type, tmdb_id, overview, genres,
			       vote_average, runtime, poster_url, url, code, shared_at, release_date,
			       `+publicGroupExpr+` AS grp
			FROM links WHERE `+publicWhere+`
		), filtered AS (
			SELECT * FROM pub WHERE 1=1`+filter+`
		), ranked AS (
			SELECT f.*, c.link_count,
			       ROW_NUMBER() OVER (
			           PARTITION BY f.grp
			           ORDER BY CASE WHEN f.shared_at = '' THEN 1 ELSE 0 END, f.shared_at DESC, f.id DESC
			       ) AS rn
			FROM filtered f
			JOIN (SELECT grp, COUNT(*) AS link_count FROM pub GROUP BY grp) c ON c.grp = f.grp
		)
		SELECT id, title, original_title, year, media_type, tmdb_id, overview, genres,
		       vote_average, runtime, poster_url, url, code, shared_at, release_date, link_count, grp
		FROM ranked WHERE rn = 1
		ORDER BY `+order+` LIMIT ? OFFSET ?`, // #nosec G202
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]model.PublicLink, 0, limit)
	groups := make([]string, 0, limit)
	for rows.Next() {
		var p model.PublicLink
		var grp string
		if err := rows.Scan(
			&p.ID, &p.Title, &p.OriginalTitle, &p.Year, &p.MediaType, &p.TMDBID,
			&p.Overview, &p.Genres, &p.VoteAverage, &p.Runtime, &p.PosterURL,
			&p.URL, &p.Code, &p.SharedAt, &p.ReleaseDate, &p.LinkCount, &grp,
		); err != nil {
			return nil, 0, err
		}
		p.URL = safeLinkURL(p.URL)
		items = append(items, p)
		groups = append(groups, grp)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// 连接池只有一条连接，先释放当前结果集再查链接，避免嵌套查询互相等待
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	links, err := s.publicLinks(groups)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		items[i].Links = links[groups[i]]
	}
	return items, total, nil
}

// publicFilterClause 只拼接额外筛选条件（不含收录状态），返回以 " AND " 开头、
// 可直接追加到 WHERE 后面的片段。分类与年份各自由调用方决定是否带上。
func publicFilterClause(f model.PublicFilter, withMedia, withYear bool) (string, []any) {
	where := []string{}
	args := []any{}

	if withMedia && f.MediaType != "" {
		where = append(where, "media_type = ?")
		args = append(args, string(f.MediaType))
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		where = append(where, `(title LIKE ? ESCAPE '\' OR original_title LIKE ? ESCAPE '\' OR genres LIKE ? ESCAPE '\')`)
		like := "%" + likeEscape(kw) + "%"
		args = append(args, like, like, like)
	}
	if withYear && f.Year > 0 {
		where = append(where, "year = ?")
		args = append(args, f.Year)
	}
	if len(where) == 0 {
		return "", args
	}
	return " AND " + strings.Join(where, " AND "), args
}

// publicLinks 一次取回页内所有分组的分享链接，组内按最近分享排前面。
func (s *Store) publicLinks(groups []string) (map[string][]model.PublicShare, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(groups)), ",")
	args := make([]any, len(groups))
	for i, g := range groups {
		args[i] = g
	}
	rows, err := s.db.Query(
		`SELECT `+publicGroupExpr+`, id, url, code, shared_at FROM links
		 WHERE `+publicWhere+` AND `+publicGroupExpr+` IN (`+placeholders+`)
		 ORDER BY CASE WHEN shared_at = '' THEN 1 ELSE 0 END, shared_at DESC, id DESC`, // #nosec G202 -- 表达式为本文件常量，值走占位符
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]model.PublicShare, len(groups))
	for rows.Next() {
		var grp string
		var sh model.PublicShare
		if err := rows.Scan(&grp, &sh.ID, &sh.URL, &sh.Code, &sh.SharedAt); err != nil {
			return nil, err
		}
		sh.URL = safeLinkURL(sh.URL)
		out[grp] = append(out[grp], sh)
	}
	return out, rows.Err()
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
// 这样每一组选项都反映「切换过去会得到多少条」。计数单位与列表一致，是 TMDB 分组数。
func (s *Store) PublicStats(f model.PublicFilter) (model.PublicStats, error) {
	var out model.PublicStats

	filter, args := publicFilterClause(f, false, true)
	// CTE 必须带上筛选条件引用的全部列：带 media_type 或关键词时，
	// 只 select 部分列的 CTE 会让 WHERE 里的列名解析失败，整个统计接口 500。
	if err := s.db.QueryRow(
		`WITH pub AS (
			SELECT media_type, year, title, original_title, genres, `+publicGroupExpr+` AS grp
			FROM links WHERE `+publicWhere+`
		)
		SELECT COUNT(DISTINCT grp),
		       COUNT(DISTINCT CASE WHEN media_type = 'movie' THEN grp END),
		       COUNT(DISTINCT CASE WHEN media_type = 'tv' THEN grp END)
		FROM pub WHERE 1=1`+filter, // #nosec G202 -- 表达式为本文件常量，值走占位符
		args...).Scan(&out.Total, &out.Movies, &out.TV); err != nil {
		return out, err
	}

	if err := s.db.QueryRow(
		`SELECT COUNT(DISTINCT ` + publicGroupExpr + `) FROM links WHERE ` + publicWhere).Scan(&out.LibraryTotal); err != nil {
		return out, err
	}

	filter, args = publicFilterClause(f, true, false)
	rows, err := s.db.Query(
		`WITH pub AS (
			SELECT year, media_type, title, original_title, genres, `+publicGroupExpr+` AS grp
			FROM links WHERE `+publicWhere+` AND year > 0
		)
		SELECT year, COUNT(DISTINCT grp) FROM pub WHERE 1=1`+filter+`
		GROUP BY year ORDER BY year DESC`, // #nosec G202 -- 表达式为本文件常量，值走占位符
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

// GetPublic 按任意一条分享的 ID 返回整组资源；Links 带出组内全部分享。
func (s *Store) GetPublic(id int64) (*model.PublicLink, error) {
	var grp string
	if err := s.db.QueryRow(
		`SELECT `+publicGroupExpr+` FROM links WHERE id = ? AND `+publicWhere, id).Scan(&grp); err != nil {
		return nil, err
	}

	var p model.PublicLink
	err := s.db.QueryRow(
		`SELECT `+publicColumns+`,
		        COUNT(*) OVER (PARTITION BY `+publicGroupExpr+`)
		 FROM links
		 WHERE `+publicWhere+` AND `+publicGroupExpr+` = ?
		 ORDER BY CASE WHEN shared_at = '' THEN 1 ELSE 0 END, shared_at DESC, id DESC
		 LIMIT 1`, // #nosec G202 -- 表达式为本文件常量，值走占位符
		grp,
	).Scan(
		&p.ID, &p.Title, &p.OriginalTitle, &p.Year, &p.MediaType, &p.TMDBID,
		&p.Overview, &p.Genres, &p.VoteAverage, &p.Runtime, &p.PosterURL,
		&p.URL, &p.Code, &p.SharedAt, &p.ReleaseDate, &p.LinkCount,
	)
	if err != nil {
		return nil, err
	}
	p.URL = safeLinkURL(p.URL)

	links, err := s.publicLinks([]string{grp})
	if err != nil {
		return nil, err
	}
	p.Links = links[grp]
	return &p, nil
}

// Sitemap 返回站点地图需要的数据：offset/limit 定位分片，同时给出分组总数与全站
// 最近更新时间。每条资源取组内主链接的 ID，lastmod 取组内最近一次更新时间。
// updated_at 用 CAST 转成文本，避免驱动按时间类型解析时踩到格式差异。
func (s *Store) Sitemap(offset, limit int) (*model.SitemapResult, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultSitemapLimit
	}
	out := &model.SitemapResult{}

	// clause 只由本文件常量拼出，limit/offset 走 ? 占位符
	where := publicWhere // #nosec G202
	if err := s.db.QueryRow(
		`SELECT COUNT(DISTINCT `+publicGroupExpr+`), COALESCE(MAX(CAST(updated_at AS TEXT)), '')
		 FROM links WHERE `+where).
		Scan(&out.Total, &out.Latest); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(
		`WITH marked AS (
			SELECT id, shared_at, year, release_date, vote_average,
			       CAST(updated_at AS TEXT) AS updated_at,
			       `+publicGroupExpr+` AS grp
			FROM links WHERE `+where+`
		), ranked AS (
			SELECT *,
			       MAX(updated_at) OVER (PARTITION BY grp) AS lastmod,
			       ROW_NUMBER() OVER (
			           PARTITION BY grp
			           ORDER BY CASE WHEN shared_at = '' THEN 1 ELSE 0 END, shared_at DESC, id DESC
			       ) AS rn
			FROM marked
		)
		SELECT id, lastmod FROM ranked WHERE rn = 1
		ORDER BY `+publicOrder["new"]+` LIMIT ? OFFSET ?`, // #nosec G202
		limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out.Entries = make([]model.SitemapEntry, 0, limit)
	for rows.Next() {
		var e model.SitemapEntry
		if err := rows.Scan(&e.ID, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, e)
	}
	return out, rows.Err()
}
