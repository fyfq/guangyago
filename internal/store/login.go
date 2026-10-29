package store

import (
	"database/sql"
	"errors"
	"time"
)

// LoginAttempt 是一条登录尝试记录（IP 或用户名维度）。
type LoginAttempt struct {
	Key   string
	Fails int
	Until time.Time
}

// LoginAttemptGet 读取指定 key 的当前锁定状态；不存在或未锁定时 Until 为零值。
func (s *Store) LoginAttemptGet(key string) (LoginAttempt, error) {
	var (
		fails   int
		untilTS int64
	)
	err := s.db.QueryRow(
		`SELECT fails, until_ts FROM login_attempts WHERE key = ?`, key,
	).Scan(&fails, &untilTS)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginAttempt{Key: key}, nil
	}
	if err != nil {
		return LoginAttempt{}, err
	}
	a := LoginAttempt{Key: key, Fails: fails}
	if untilTS > 0 {
		a.Until = time.Unix(untilTS, 0)
	}
	return a, nil
}

// LoginAttemptFail 递增失败次数，达到 maxFails 时把 Until 设为 now+lockFor。
// 依赖 SQLite 单写连接（SetMaxOpenConns=1）保证读改写事务串行化。
func (s *Store) LoginAttemptFail(key string, now time.Time, maxFails int, lockFor time.Duration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		fails   int
		untilTS int64
	)
	err = tx.QueryRow(`SELECT fails, until_ts FROM login_attempts WHERE key = ?`, key).
		Scan(&fails, &untilTS)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if untilTS > 0 && now.After(time.Unix(untilTS, 0)) {
		fails = 0
		untilTS = 0
	}
	fails++
	if fails >= maxFails {
		fails = 0
		untilTS = now.Add(lockFor).Unix()
	}
	if _, err := tx.Exec(
		`INSERT INTO login_attempts(key, fails, until_ts, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET fails=excluded.fails, until_ts=excluded.until_ts, updated_at=excluded.updated_at`,
		key, fails, untilTS, now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// LoginAttemptReset 清除指定 key 的失败计数与锁定状态。
func (s *Store) LoginAttemptReset(key string) error {
	_, err := s.db.Exec(`DELETE FROM login_attempts WHERE key = ?`, key)
	return err
}

// LoginAttemptCleanup 删除锁定已到期且计数已清零的记录。
func (s *Store) LoginAttemptCleanup(now time.Time) error {
	_, err := s.db.Exec(
		`DELETE FROM login_attempts WHERE until_ts > 0 AND until_ts < ? AND fails = 0`,
		now.Unix())
	return err
}
