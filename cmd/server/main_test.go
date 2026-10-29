package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// requestLog 按状态码分级并输出固定字段，便于按 path/status 检索。
func TestRequestLog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(requestLog())
	r.GET("/api/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/demo", func(c *gin.Context) { c.Status(http.StatusTeapot) })
	r.GET("/api/boom", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })

	cases := []struct{ path, level, status string }{
		{"/api/health", "level=DEBUG", "status=200"},
		{"/api/demo", "level=WARN", "status=418"},
		{"/api/boom", "level=ERROR", "status=500"},
	}
	for _, tc := range cases {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.RemoteAddr = "203.0.113.7:4567"
		r.ServeHTTP(httptest.NewRecorder(), req)
		out := buf.String()
		for _, want := range []string{tc.level, tc.status, "path=" + tc.path, "ip=203.0.113.7", "latency_ms="} {
			if !strings.Contains(out, want) {
				t.Errorf("%s 日志缺少 %q：%s", tc.path, want, out)
			}
		}
	}
}
