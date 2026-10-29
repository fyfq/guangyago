package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/odin/guangya-go/internal/auth"
	"github.com/odin/guangya-go/internal/config"
	"github.com/odin/guangya-go/internal/model"
)

// setupStatus 返回系统安装状态
func (s *Server) setupStatus(c *gin.Context) {
	n, err := s.store.CountUsers()
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"installed": n > 0,
	})
}

type setupRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	TMDBAPIKey  string `json:"tmdb_api_key"`
}

// setup 完成初始安装，创建管理员账号
func (s *Server) setup(c *gin.Context) {
	// 检查是否已安装
	n, err := s.store.CountUsers()
	if err != nil {
		serverError(c, err)
		return
	}
	if n > 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "系统已安装，请勿重复操作"})
		return
	}

	var req setupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if !usernameRe.MatchString(req.Username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名 3-32 位，仅限字母、数字、下划线、点和短横线"})
		return
	}
	if !validPassword(req.Password) {
		c.JSON(http.StatusBadRequest, gin.H{"error": passwordHintFor(req.Password)})
		return
	}
	if len(req.DisplayName) > 40 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "显示名过长"})
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = "管理员"
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}

	u := &model.User{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Role:        model.RoleAdmin,
		Enabled:     true,
	}
	if err := s.store.CreateUser(u, hash); err != nil {
		serverError(c, err)
		return
	}

	slog.Info("安装完成，管理员已创建", "username", u.Username)

	// 如果提供了 TMDB API Key，保存到配置
	if strings.TrimSpace(req.TMDBAPIKey) != "" && s.cfg != nil && s.configPath != "" {
		s.configMu.Lock()
		s.cfg.TMDB.APIKey = req.TMDBAPIKey
		saveErr := config.Save(s.configPath, s.cfg)
		s.configMu.Unlock()
		if saveErr != nil {
			slog.Warn("保存 TMDB API Key 失败", "err", saveErr)
		} else if s.reload != nil {
			s.reload(s.cfg)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"user_id": u.ID,
	})
}
