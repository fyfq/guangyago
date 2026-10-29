package model

import "time"

// 后台账号角色：admin 可管理用户，operator 只能使用流水线功能。
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
)

// User 是后台登录账号，密码哈希只存在于存储层，不参与序列化。
type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	Enabled     bool       `json:"enabled"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// PublicLink 是对外门户暴露的最小字段集：只够展示与取链接，不含内部状态与错误信息。
type PublicLink struct {
	ID            int64     `json:"id"`
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
	URL           string    `json:"url"`
	Code          string    `json:"code"`
	SharedAt      string    `json:"shared_at"`
}

// PublicFilter 是门户列表的查询条件。
type PublicFilter struct {
	Keyword   string
	MediaType MediaType
	Year      int
	Sort      string
	Limit     int
	Offset    int
}

// PublicYearStat 是年份筛选项中某一年的资源数量。
type PublicYearStat struct {
	Year  int `json:"year"`
	Count int `json:"count"`
}

// PublicStats 是门户筛选栏要用的计数，各项都在其他筛选条件固定时统计。
type PublicStats struct {
	Total        int              `json:"total"`
	Movies       int              `json:"movies"`
	TV           int              `json:"tv"`
	LibraryTotal int              `json:"library_total"`
	Years        []PublicYearStat `json:"years"`
}
