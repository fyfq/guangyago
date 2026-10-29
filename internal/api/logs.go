package api

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// downloadLog 把当前滚动日志文件作为附件返回给管理员。
// 只读启动时注入的固定路径，不接受客户端指定文件名，避免路径穿越。
func (s *Server) downloadLog(c *gin.Context) {
	if s.logPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "日志文件未启用"})
		return
	}
	fi, err := os.Stat(s.logPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "日志文件不存在"})
		return
	}
	username := ""
	if u := currentUser(c); u != nil {
		username = u.Username
	}
	slog.Info("管理员下载日志", "username", username, "size", fi.Size())
	c.FileAttachment(s.logPath, "guangya-"+time.Now().Format("20060102-150405")+".log")
}
