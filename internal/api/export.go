package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/model"
)

// exportLinks 按当前筛选条件导出 CSV（带 BOM，Excel 直接打开不乱码）。
func (s *Server) exportLinks(c *gin.Context) {
	f := model.ListFilter{
		Status:  model.Status(c.Query("status")),
		Keyword: c.Query("q"),
		Limit:   10000,
	}
	links, _, err := s.store.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="guangya-links.csv"`)
	c.Writer.Write([]byte("\xef\xbb\xbf"))
	writer := csvWriter{c.Writer}
	writer.write([]string{
		"ID", "文件名", "片名", "年份", "TMDB ID", "链接", "提取码",
		"分享时间", "分享状态", "处理状态", "推送消息ID",
	})
	for _, l := range links {
		writer.write([]string{
			strconv.FormatInt(l.ID, 10),
			l.RawTitle,
			l.Title,
			strconv.Itoa(l.Year),
			strconv.FormatInt(l.TMDBID, 10),
			l.URL,
			l.Code,
			l.SharedAt,
			l.ShareStatus,
			string(l.Status),
			strconv.FormatInt(l.TGMessageID, 10),
		})
	}
}

type csvWriter struct{ w http.ResponseWriter }

func (cw csvWriter) write(fields []string) {
	for i, f := range fields {
		if i > 0 {
			cw.w.Write([]byte(","))
		}
		cw.w.Write([]byte(quoteCSV(f)))
	}
	cw.w.Write([]byte("\r\n"))
}

func quoteCSV(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// stats 返回各状态数量，供页面顶部概览使用。
func (s *Server) stats(c *gin.Context) {
	counts := map[string]int{}
	for _, st := range []model.Status{
		model.StatusPending, model.StatusReview, model.StatusScraping,
		model.StatusScraped, model.StatusUnmatched, model.StatusPushed,
		model.StatusPushFailed, model.StatusFailed,
	} {
		_, total, err := s.store.List(model.ListFilter{Status: st, Limit: 1})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		counts[string(st)] = total
	}
	c.JSON(http.StatusOK, counts)
}

// batchDelete 批量删除，请求体形如 {"ids":[1,2,3]}。
func (s *Server) batchDelete(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids 不能为空"})
		return
	}
	deleted := 0
	for _, id := range req.IDs {
		if err := s.store.Delete(id); err == nil {
			deleted++
		}
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}
