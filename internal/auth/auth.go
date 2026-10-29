// Package auth 提供后台账号的密码哈希与会话令牌工具。
package auth

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

	// SecretFileSize 是持久化会话密钥的字节长度。
	SecretKeySize = 32
	// secretFileName 是 DataDir 下存放会话密钥的文件名。
	secretFileName = "session.key"
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

// dummyPassword 每次进程启动时随机生成，避免因源码里固定的字符串带来的语义歧义；
// dummyHash 走一遍同等开销的校验，防止响应耗时泄露用户名是否存在。
var (
	dummyOnce  sync.Once
	dummyHash  string
	dummyValue string
)

func initDummy() {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况下退化为纳秒时间，仍然比固定字符串更难被利用
		dummyValue = strconv.FormatInt(time.Now().UnixNano(), 10)
	} else {
		dummyValue = base64.RawURLEncoding.EncodeToString(buf)
	}
	h, err := HashPassword(dummyValue)
	if err != nil {
		dummyHash = ""
		return
	}
	dummyHash = h
}

// VerifyPasswordOrDummy 在 encoded 为空时校验占位哈希并始终返回 false。
func VerifyPasswordOrDummy(encoded, password string) bool {
	dummyOnce.Do(initDummy)
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

// sessionSecret 用于 HMAC-SHA256 令牌摘要，进程启动时由 SetSessionSecret 注入；
// 未注入时 HashToken 退化到旧的裸 SHA-256，保证升级路径不断档。
var (
	secretMu   sync.RWMutex
	sessionKey []byte
)

// SetSessionSecret 设置会话摘要用的 HMAC 密钥；长度为 0 时视为未启用。
func SetSessionSecret(key []byte) {
	secretMu.Lock()
	sessionKey = append([]byte(nil), key...)
	secretMu.Unlock()
}

// HashToken 对会话令牌做 HMAC-SHA256（未注入密钥时退化为 SHA-256），数据库里只存摘要。
func HashToken(token string) string {
	secretMu.RLock()
	key := sessionKey
	secretMu.RUnlock()
	if len(key) == 0 {
		sum := sha256.Sum256([]byte(token))
		return base64.RawURLEncoding.EncodeToString(sum[:])
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(token))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// LoadOrCreateSessionKey 从 dataDir 读取会话密钥；不存在时生成一份并落盘（0o600）。
// 用于「升级即启用 HMAC」且保持进程重启后仍能校验既有会话。
func LoadOrCreateSessionKey(dataDir string) ([]byte, error) {
	if dataDir == "" {
		return nil, errors.New("dataDir 为空")
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, secretFileName)
	b, err := os.ReadFile(path)
	if err == nil && len(b) == SecretKeySize {
		return b, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, SecretKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, key, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return key, nil
}
