package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/model"
)

var errBadMediaType = errors.New("media_type 只能是 movie 或 tv")

// portalFilter 解析门户接口共用的筛选参数。
func portalFilter(c *gin.Context) (model.PublicFilter, error) {
	keyword := strings.TrimSpace(c.Query("q"))
	if utf8.RuneCountInString(keyword) > 64 {
		keyword = string([]rune(keyword)[:64])
	}

	mediaType := model.MediaType(strings.TrimSpace(c.Query("media_type")))
	if mediaType != "" && mediaType != model.MediaMovie && mediaType != model.MediaTV {
		return model.PublicFilter{}, errBadMediaType
	}

	// 年份只是展示筛选，非法值直接当作「全部年份」，不打断请求
	year, err := strconv.Atoi(strings.TrimSpace(c.Query("year")))
	if err != nil || year < 0 || year > 9999 {
		year = 0
	}

	return model.PublicFilter{
		Keyword:   keyword,
		MediaType: mediaType,
		Year:      year,
		Sort:      c.Query("sort"),
	}, nil
}

// portalLinks 是公开的资源门户列表，只读且只看已推送的记录。
func (s *Server) portalLinks(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "24"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 60 {
		limit = 24
	}
	// 深分页对公开接口没有意义，顺带限制 offset 防止被用来消耗数据库
	if offset < 0 {
		offset = 0
	}
	if offset > 10000 {
		offset = 10000
	}

	f, err := portalFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不正确"})
		return
	}
	f.Limit = limit
	f.Offset = offset

	items, total, err := s.store.ListPublic(f)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": total, "items": items})
}

func (s *Server) portalStats(c *gin.Context) {
	f, err := portalFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不正确"})
		return
	}
	stats, err := s.store.PublicStats(f)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, stats)
}
