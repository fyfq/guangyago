package model

import "time"

// Status 表示一个链接在处理管道中的状态。
type Status string

const (
	StatusPending    Status = "pending"     // 已入库，等待刮削
	StatusReview     Status = "review"      // 分享状态异常，待人工核查
	StatusScraping   Status = "scraping"    // 刮削中
	StatusScraped    Status = "scraped"     // 已刮削，待推送
	StatusUnmatched  Status = "unmatched"   // 未匹配到 TMDB 条目
	StatusPushed     Status = "pushed"      // 已推送到频道
	StatusPushFailed Status = "push_failed" // 推送失败，可重推
	StatusFailed     Status = "failed"      // 解析或刮削失败
)

// MediaType 区分 TMDB 的电影与剧集。
type MediaType string

const (
	MediaMovie MediaType = "movie"
	MediaTV    MediaType = "tv"
)

// Link 是一条光鸭分享链接的完整记录。
type Link struct {
	ID            int64     `json:"id"`
	ShareID       string    `json:"share_id"`
	URL           string    `json:"url"`
	Code          string    `json:"code"`
	Source        string    `json:"source"`
	RawTitle      string    `json:"raw_title"`
	Title         string    `json:"title"`
	OriginalTitle string    `json:"original_title"`
	Year          int       `json:"year"`
	MediaType     MediaType `json:"media_type"`
	TMDBID        int64     `json:"tmdb_id"`
	Overview      string    `json:"overview"`
	Genres        string    `json:"genres"`
	VoteAverage   float64   `json:"vote_average"`
	Runtime       int       `json:"runtime"`
	PosterURL     string    `json:"poster_url"`
	PosterPath    string    `json:"poster_path"`
	SharedAt      string    `json:"shared_at"`
	ShareStatus   string    `json:"share_status"`
	Status        Status    `json:"status"`
	Error         string    `json:"error"`
	TGMessageID   int64     `json:"tg_message_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ListFilter 用于链接列表查询。
// Statuses 支持一次传多个状态（任一匹配即可），为空时再看 Status 单值。
type ListFilter struct {
	Status   Status
	Statuses []Status
	Keyword  string
	Limit    int
	Offset   int
}

// StatusSet 把常见状态集合定义为常量，避免各页面各自拼写。
var (
	// PendingScrape 是尚未刮削成功的全部状态，刮削队列页专用。
	PendingScrape = []Status{StatusPending, StatusReview, StatusScraping, StatusUnmatched, StatusFailed}
	// ScrapedOnly 是刮削成功的全部状态，资源库页专用。
	ScrapedOnly = []Status{StatusScraped, StatusPushed, StatusPushFailed}
)
