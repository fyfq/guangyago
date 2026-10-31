package model

// 资源互动事件：浏览海报进详情、复制分享链接、点开分享链接。
// 三类分开计数，门户与后台统计都按这个口径展示。
const (
	ActionBrowse = "browse"
	ActionCopy   = "copy"
	ActionOpen   = "open"
)

// ShareStats 是一条分享链接的累计互动次数，只在后台统计详情里填充。
type ShareStats struct {
	Browses int64
	Copies  int64
	Opens   int64
}

// TrafficDay 是门户流量趋势里的一天：访问（PV）、访客（UV）与三类互动次数。
type TrafficDay struct {
	Date    string `json:"date"`
	PV      int    `json:"pv"`
	UV      int    `json:"uv"`
	Browses int    `json:"browses"`
	Copies  int    `json:"copies"`
	Opens   int    `json:"opens"`
}

// TrafficLink 是热门资源排行里的一条，只带展示所需字段。
type TrafficLink struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	MediaType MediaType `json:"media_type"`
	TMDBID    int64     `json:"tmdb_id"`
	PosterURL string    `json:"poster_url"`
	Browses   int       `json:"browses"`
	Copies    int       `json:"copies"`
	Opens     int       `json:"opens"`
}

// PortalTraffic 是后台统计页需要的全部门户数据。
type PortalTraffic struct {
	Days         int           `json:"days"`
	TodayPV      int           `json:"today_pv"`
	TodayUV      int           `json:"today_uv"`
	RangePV      int           `json:"range_pv"`
	RangeUV      int           `json:"range_uv"`
	RangeBrowses int           `json:"range_browses"`
	RangeCopies  int           `json:"range_copies"`
	RangeOpens   int           `json:"range_opens"`
	TotalBrowses int           `json:"total_browses"`
	TotalCopies  int           `json:"total_copies"`
	TotalOpens   int           `json:"total_opens"`
	Trend        []TrafficDay  `json:"trend"`
	Top          []TrafficLink `json:"top"`
}
