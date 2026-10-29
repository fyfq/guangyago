package api

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/model"
)

// exportLinks 按当前筛选条件导出 CSV（带 BOM，Excel 直接打开不乱码）。
func (s *Server) exportLinks(c *gin.Context) {
	f := model.ListFilter{Keyword: c.Query("q"), Limit: 10000}
	applyStatusFilter(&f, c.Query("status"))
	links, _, err := s.store.List(f)
	if err != nil {
		serverError(c, err)
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
	// 以 = + - @ 等开头的字段会被 Excel 当公式执行，加前导单引号按文本处理；
	// 前导空白可能被表格软件忽略，因此跳过空白后再判断。
	trimmed := strings.TrimLeft(s, " \t\r")
	first, _ := utf8.DecodeRuneInString(trimmed)
	if trimmed != "" && strings.ContainsRune("=+-@\t\r", first) {
		s = "'" + s
	}
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// stats 返回各状态数量，供页面顶部概览使用。
func (s *Server) stats(c *gin.Context) {
	counts, err := s.store.CountByStatus()
	if err != nil {
		serverError(c, err)
		return
	}
	// 固定输出全部状态，计数为 0 的也返回，前端无需再补默认值
	out := make(map[string]int, len(counts)+1)
	total := 0
	for _, n := range counts {
		total += n
	}
	// total 按库内实际状态汇总（含未知状态），与 /api/links 的 {total,items} 契约对齐，
	// 前端不必自己把各状态相加。
	out["total"] = total
	for _, st := range []model.Status{
		model.StatusPending, model.StatusReview, model.StatusScraping,
		model.StatusScraped, model.StatusUnmatched, model.StatusPushed,
		model.StatusPushFailed, model.StatusFailed,
	} {
		out[string(st)] = counts[st]
	}
	c.JSON(http.StatusOK, out)
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
	deleted, err := s.store.DeleteMany(req.IDs)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}
