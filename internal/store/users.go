package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/odin/guangya-go/internal/model"
)

// userColumns 是用户表的查询字段，密码哈希单独按需读取。
const userColumns = `id, username, display_name, role, enabled, last_login_at, created_at`

// userColumnsQualified 用于与 sessions 联表时避免同名列歧义。
const userColumnsQualified = `u.id, u.username, u.display_name, u.role, u.enabled, u.last_login_at, u.created_at`

func scanUser(row scanner) (*model.User, error) {
	u := &model.User{}
	var lastLogin sql.NullString
	var createdAt string
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Enabled, &lastLogin, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastLogin.Valid && lastLogin.String != "" {
		t := parseTime(lastLogin.String)
		if !t.IsZero() {
			u.LastLoginAt = &t
		}
	}
	u.CreatedAt = parseTime(createdAt)
	return u, nil
}

// CountUsers 用于首次启动时判断是否需要初始化管理员。
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser 插入账号，username 冲突时返回错误。
func (s *Store) CreateUser(u *model.User, passwordHash string) error {
	res, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, display_name, role, enabled) VALUES (?,?,?,?,?)`,
		u.Username, passwordHash, u.DisplayName, u.Role, u.Enabled)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err == nil {
		u.ID = id
		u.CreatedAt = time.Now()
	}
	return err
}

// UserByUsername 同时返回密码哈希，仅在登录校验时使用。
func (s *Store) UserByUsername(username string) (*model.User, string, error) {
	row := s.db.QueryRow(`SELECT `+userColumns+`, password_hash FROM users WHERE username = ?`, username)
	u := &model.User{}
	var lastLogin sql.NullString
	var createdAt, hash string
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Enabled, &lastLogin, &createdAt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if lastLogin.Valid && lastLogin.String != "" {
		if t := parseTime(lastLogin.String); !t.IsZero() {
			u.LastLoginAt = &t
		}
	}
	u.CreatedAt = parseTime(createdAt)
	return u, hash, nil
}

// UserByID 按主键读取账号。
func (s *Store) UserByID(id int64) (*model.User, error) {
	row := s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	return scanUser(row)
}

// UserPasswordHash 读取指定账号的密码哈希，用于自行改密时校验旧密码。
func (s *Store) UserPasswordHash(id int64) (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return hash, err
}

// ListUsers 按创建顺序返回全部账号。
func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]model.User, 0, 8)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// UpdateUser 更新显示名、角色与启用状态。
func (s *Store) UpdateUser(id int64, displayName, role string, enabled bool) error {
	_, err := s.db.Exec(
		`UPDATE users SET display_name = ?, role = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		displayName, role, enabled, id)
	return err
}

// UpdateUserPassword 写入新的密码哈希。
func (s *Store) UpdateUserPassword(id int64, passwordHash string) error {
	_, err := s.db.Exec(
		`UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		passwordHash, id)
	return err
}

// TouchLastLogin 记录最近登录时间。
func (s *Store) TouchLastLogin(id int64) error {
	_, err := s.db.Exec(`UPDATE users SET last_login_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// DeleteUser 删除账号，并清掉其全部会话。
func (s *Store) DeleteUser(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	return s.DeleteUserSessions(id)
}

// EnabledAdminCount 统计除 excludeID 外仍然启用的管理员数量，用于保住最后一个管理员。
func (s *Store) EnabledAdminCount(excludeID int64) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM users WHERE role = ? AND enabled = 1 AND id <> ?`,
		model.RoleAdmin, excludeID).Scan(&n)
	return n, err
}

// CreateSession 保存会话摘要。
func (s *Store) CreateSession(userID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?,?,?)`,
		tokenHash, userID, expiresAt.Unix())
	return err
}

// SessionUser 用令牌摘要换回账号；过期或账号被停用都视为无效。
func (s *Store) SessionUser(tokenHash string, now time.Time) (*model.User, error) {
	row := s.db.QueryRow(`SELECT `+userColumnsQualified+` FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ? AND s.expires_at > ? AND u.enabled = 1`, tokenHash, now.Unix())
	return scanUser(row)
}

// DeleteSession 注销单个会话。
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions 清除某账号的全部会话，停用与改密后立即生效。
func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// DeleteExpiredSessions 清理过期会话，登录时顺带执行一次。
func (s *Store) DeleteExpiredSessions(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	return err
}
