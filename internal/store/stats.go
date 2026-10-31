package store

import (
	"strings"
	"time"

	"github.com/odin/guangya-go/internal/model"
)

// dayLayout 是统计明细按天聚合用的日期格式（服务器本地时区）。
const dayLayout = "2006-01-02"

// DayKey 把时间截成统计用的日期键。
func DayKey(t time.Time) string { return t.Format(dayLayout) }

// RecordVisit 给某个访客当天的访问次数 +1。
func (s *Store) RecordVisit(day, visitorHash string) error {
	_, err := s.db.Exec(
		`INSERT INTO portal_visits(day, visitor_hash, views) VALUES(?,?,1)
		 ON CONFLICT(day, visitor_hash) DO UPDATE SET views = views + 1`,
		day, visitorHash)
	return err
}

// RecordAction 给某条分享当天的某类互动（浏览/复制/打开）+1。
func (s *Store) RecordAction(linkID int64, day, action string) error {
	_, err := s.db.Exec(
		`INSERT INTO resource_events(link_id, day, action, hits) VALUES(?,?,?,1)
		 ON CONFLICT(link_id, day, action) DO UPDATE SET hits = hits + 1`,
		linkID, day, action)
	return err
}

// ShareActions 返回指定分享链接的累计互动次数，没有记录的 ID 不出现在结果里。
func (s *Store) ShareActions(ids []int64) (map[int64]model.ShareStats, error) {
	out := make(map[int64]model.ShareStats, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT link_id, action, SUM(hits) FROM resource_events
		 WHERE link_id IN (`+placeholders+`) GROUP BY link_id, action`, // #nosec G202 -- 只有占位符
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id     int64
			action string
			hits   int64
		)
		if err := rows.Scan(&id, &action, &hits); err != nil {
			return nil, err
		}
		st := out[id]
		switch action {
		case model.ActionBrowse:
			st.Browses = hits
		case model.ActionCopy:
			st.Copies = hits
		case model.ActionOpen:
			st.Opens = hits
		}
		out[id] = st
	}
	return out, rows.Err()
}

// PurgeTraffic 删除早于 beforeDay 的明细，返回删除的总行数。
func (s *Store) PurgeTraffic(beforeDay string) (int64, error) {
	visits, err := s.db.Exec(`DELETE FROM portal_visits WHERE day < ?`, beforeDay)
	if err != nil {
		return 0, err
	}
	events, err := s.db.Exec(`DELETE FROM resource_events WHERE day < ?`, beforeDay)
	if err != nil {
		return 0, err
	}
	nv, err := visits.RowsAffected()
	if err != nil {
		return 0, err
	}
	ne, err := events.RowsAffected()
	if err != nil {
		return 0, err
	}
	return nv + ne, nil
}

// PortalTraffic 汇总最近 days 天的门户数据：按日趋势、区间合计与热门资源。
// 区间内没有数据的日期也会补零，前端不需要自己补齐。
func (s *Store) PortalTraffic(days int, today time.Time) (*model.PortalTraffic, error) {
	if days <= 0 {
		days = 30
	}
	out := &model.PortalTraffic{Days: days}

	start := today.AddDate(0, 0, -(days - 1))
	dates := make([]string, 0, days)
	index := make(map[string]int, days)
	for i := 0; i < days; i++ {
		d := DayKey(start.AddDate(0, 0, i))
		index[d] = len(dates)
		dates = append(dates, d)
		out.Trend = append(out.Trend, model.TrafficDay{Date: d})
	}

	startDay := dates[0]
	rows, err := s.db.Query(
		`SELECT day, COALESCE(SUM(views), 0), COUNT(*) FROM portal_visits
		 WHERE day >= ? GROUP BY day`, startDay)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			day   string
			views int
			uv    int
		)
		if err := rows.Scan(&day, &views, &uv); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[day]; ok {
			out.Trend[i].PV = views
			out.Trend[i].UV = uv
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	rows, err = s.db.Query(
		`SELECT day, action, COALESCE(SUM(hits), 0) FROM resource_events
		 WHERE day >= ? GROUP BY day, action`, startDay)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			day    string
			action string
			hits   int
		)
		if err := rows.Scan(&day, &action, &hits); err != nil {
			rows.Close()
			return nil, err
		}
		i, ok := index[day]
		if !ok {
			continue
		}
		switch action {
		case model.ActionBrowse:
			out.Trend[i].Browses = hits
		case model.ActionCopy:
			out.Trend[i].Copies = hits
		case model.ActionOpen:
			out.Trend[i].Opens = hits
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	for _, d := range out.Trend {
		out.RangePV += d.PV
		out.RangeBrowses += d.Browses
		out.RangeCopies += d.Copies
		out.RangeOpens += d.Opens
	}
	// 区间 UV 是窗口内去重的访客数，不能把每日 UV 直接相加
	if err := s.db.QueryRow(
		`SELECT COUNT(DISTINCT visitor_hash) FROM portal_visits WHERE day >= ?`,
		startDay).Scan(&out.RangeUV); err != nil {
		return nil, err
	}
	if i, ok := index[DayKey(today)]; ok {
		out.TodayPV = out.Trend[i].PV
		out.TodayUV = out.Trend[i].UV
	}

	if err := s.db.QueryRow(
		`SELECT
			COALESCE(SUM(CASE WHEN action = ? THEN hits END), 0),
			COALESCE(SUM(CASE WHEN action = ? THEN hits END), 0),
			COALESCE(SUM(CASE WHEN action = ? THEN hits END), 0)
		 FROM resource_events`,
		model.ActionBrowse, model.ActionCopy, model.ActionOpen,
	).Scan(&out.TotalBrowses, &out.TotalCopies, &out.TotalOpens); err != nil {
		return nil, err
	}

	// JOIN 而不是 LEFT JOIN：资源被删除后，它的历史行不再出现在排行里，
	// 但区间与累计互动数仍按明细统计，不做回溯修正。
	// 同一 TMDB 的多条分享合并成一条，三类动作次数分别相加；代表行取组内最近分享的那条。
	rows, err = s.db.Query(
		`WITH detail AS (
			SELECT l.id, l.title, l.year, l.media_type, l.tmdb_id, l.poster_url, l.shared_at,
			       e.action, e.hits, ` + publicGroupExpr + ` AS grp
			FROM resource_events e JOIN links l ON l.id = e.link_id
		), agg AS (
			SELECT grp,
			       SUM(CASE WHEN action = '` + model.ActionBrowse + `' THEN hits ELSE 0 END) AS browses,
			       SUM(CASE WHEN action = '` + model.ActionCopy + `' THEN hits ELSE 0 END) AS copies,
			       SUM(CASE WHEN action = '` + model.ActionOpen + `' THEN hits ELSE 0 END) AS opens
			FROM detail GROUP BY grp
		), rep AS (
			SELECT grp, id, title, year, media_type, tmdb_id, poster_url,
			       ROW_NUMBER() OVER (
			           PARTITION BY grp
			           ORDER BY CASE WHEN shared_at = '' THEN 1 ELSE 0 END, shared_at DESC, id DESC
			       ) AS rn
			FROM detail
		)
		SELECT r.id, r.title, r.year, r.media_type, r.tmdb_id, r.poster_url,
		       a.browses, a.copies, a.opens
		FROM agg a JOIN rep r ON r.grp = a.grp AND r.rn = 1
		ORDER BY (a.browses + a.copies + a.opens) DESC, r.id DESC LIMIT 20`, // #nosec G202 -- 分组表达式与动作名均为本包常量
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out.Top = make([]model.TrafficLink, 0, 20)
	for rows.Next() {
		var t model.TrafficLink
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Year, &t.MediaType, &t.TMDBID, &t.PosterURL,
			&t.Browses, &t.Copies, &t.Opens,
		); err != nil {
			return nil, err
		}
		t.PosterURL = strings.TrimSpace(t.PosterURL)
		out.Top = append(out.Top, t)
	}
	return out, rows.Err()
}
