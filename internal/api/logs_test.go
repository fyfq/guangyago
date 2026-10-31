package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/odin/gygo/internal/model"
)

// 管理员下载当前日志文件；未启用日志文件时明确返回 404。
func TestDownloadLog(t *testing.T) {
	env := newAPIEnv(t)
	if rec := env.call(http.MethodGet, "/api/logs/download", nil); rec.Code != http.StatusNotFound {
		t.Errorf("未启用日志应返回 404，实际 %d", rec.Code)
	}

	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatalf("写入日志: %v", err)
	}
	env.srv.WithLogFile(path)

	rec := env.call(http.MethodGet, "/api/logs/download", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("下载日志失败: %d %s", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".log") {
		t.Errorf("应以附件形式下载日志: %q", cd)
	}
	if rec.Body.String() != "line1\nline2\n" {
		t.Errorf("日志内容不一致: %q", rec.Body.String())
	}
}

// 非管理员不能下载日志。
func TestDownloadLog_OperatorForbidden(t *testing.T) {
	env := newAPIEnv(t)
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("写入日志: %v", err)
	}
	env.srv.WithLogFile(path)

	op := env.createUser("op", "Abcdef12!", model.RoleOperator)
	token := env.newSession(op.ID)
	if rec := env.callAs(http.MethodGet, "/api/logs/download", nil, token); rec.Code != http.StatusForbidden {
		t.Errorf("运营账号应被拒绝，实际 %d", rec.Code)
	}
}
