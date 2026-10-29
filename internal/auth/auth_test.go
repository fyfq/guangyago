package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashPassword_VerifyRoundtrip(t *testing.T) {
	encoded, err := HashPassword("hunter2-hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(encoded, "pbkdf2$210000$") {
		t.Errorf("意外哈希格式: %q", encoded)
	}
	if !VerifyPassword(encoded, "hunter2-hunter2") {
		t.Error("正确口令校验失败")
	}
	if VerifyPassword(encoded, "wrong") {
		t.Error("错误口令不应通过")
	}
}

func TestHashPassword_UniqueSalts(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Error("两次哈希不应相等（盐应随机）")
	}
}

func TestVerifyPassword_BadFormat(t *testing.T) {
	cases := []string{
		"",
		"md5$abc",
		"pbkdf2$0$a$b",
		"pbkdf2$not-int$a$b",
		"pbkdf2$1000$!!bad-base64$$",
		"pbkdf2$1000$short",
	}
	for _, s := range cases {
		if VerifyPassword(s, "x") {
			t.Errorf("应拒绝的格式: %q", s)
		}
	}
}

func TestVerifyPasswordOrDummy_EmptyHashReturnsFalse(t *testing.T) {
	if VerifyPasswordOrDummy("", "anything") {
		t.Error("encoded 为空应始终返回 false")
	}
}

func TestSetSessionSecret_HMACMode(t *testing.T) {
	defer SetSessionSecret(nil)

	// 无密钥 → 走 SHA-256 分支
	SetSessionSecret(nil)
	plain := HashToken("abc")
	if plain != base64.RawURLEncoding.EncodeToString(sha256sum("abc")) {
		t.Error("未注入密钥时应回落到裸 SHA-256")
	}

	// 注入密钥 → 走 HMAC 分支，且不同 token 摘要稳定
	SetSessionSecret([]byte("k3y-k3y-k3y"))
	once := HashToken("abc")
	twice := HashToken("abc")
	if once != twice {
		t.Error("同一密钥下相同 token 应得相同摘要")
	}
	if once == plain {
		t.Error("注入 HMAC 密钥后摘要不应等于 SHA-256 结果")
	}
	if HashToken("abd") == once {
		t.Error("不同 token 应得不同摘要")
	}
}

func TestNewSessionToken(t *testing.T) {
	token, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || hash == "" {
		t.Fatal("token/hash 均不应为空")
	}
	if token == hash {
		t.Error("入库摘要不应等于明文")
	}
	if HashToken(token) != hash {
		t.Error("HashToken(token) 应等于 NewSessionToken 返回的 hash")
	}
}

func TestLoadOrCreateSessionKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateSessionKey(""); err == nil {
		t.Error("dataDir 为空应报错")
	}

	key1, err := LoadOrCreateSessionKey(dir)
	if err != nil {
		t.Fatalf("首次加载: %v", err)
	}
	if len(key1) != SecretKeySize {
		t.Fatalf("密钥长度 %d，期望 %d", len(key1), SecretKeySize)
	}

	// 权限必须是 0600
	info, err := os.Stat(filepath.Join(dir, secretFileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("密钥文件权限 %o，期望 0600", perm)
	}

	key2, err := LoadOrCreateSessionKey(dir)
	if err != nil {
		t.Fatalf("二次加载: %v", err)
	}
	if string(key1) != string(key2) {
		t.Error("两次加载应返回同一份密钥")
	}
}

// sha256sum 用于对照 HashToken 无密钥分支的期望值。
func sha256sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
