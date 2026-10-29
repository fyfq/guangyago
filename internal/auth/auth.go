// Package auth 提供后台账号的密码哈希与会话令牌工具。
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// CookieName 是会话 Cookie 名。
	CookieName = "gy_session"
	// SessionTTL 是登录有效期。
	SessionTTL = 7 * 24 * time.Hour

	iterations = 210_000
	keyLen     = 32
	saltLen    = 16
)

var errBadHash = errors.New("密码哈希格式不正确")

// HashPassword 生成 PBKDF2-HMAC-SHA256 哈希，格式：pbkdf2$iter$salt$key（base64url）。
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2$%d$%s$%s", iterations,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(key)), nil
}

// dummyHash 用于账号不存在时也走一遍同等开销的校验，
// 避免响应耗时差异暴露用户名是否存在。
var dummyHash = func() string {
	h, err := HashPassword("guangya-dummy-password")
	if err != nil {
		return ""
	}
	return h
}()

// VerifyPasswordOrDummy 在 encoded 为空时校验占位哈希并始终返回 false。
func VerifyPasswordOrDummy(encoded, password string) bool {
	if encoded == "" {
		VerifyPassword(dummyHash, password)
		return false
	}
	return VerifyPassword(encoded, password)
}

// VerifyPassword 按哈希里记录的迭代次数重算并做常量时间比较。
func VerifyPassword(encoded, password string) bool {
	iter, salt, want, err := parseHash(encoded)
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func parseHash(encoded string) (int, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return 0, nil, nil, errBadHash
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return 0, nil, nil, errBadHash
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return 0, nil, nil, errBadHash
	}
	key, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(key) == 0 {
		return 0, nil, nil, errBadHash
	}
	return iter, salt, key, nil
}

// NewSessionToken 返回交给 Cookie 的明文令牌，以及入库前应先哈希的摘要。
func NewSessionToken() (token, tokenHash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken 对会话令牌做 SHA-256，数据库里只存摘要。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
