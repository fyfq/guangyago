// Package logging 提供按大小滚动的日志文件。
// 服务端把 slog 同时写到标准输出与这里的文件，便于在界面上直接下载排查。
package logging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Writer 把日志追加到 dir/app.log，超过上限后滚动为 app.log.1 … app.log.N。
type Writer struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	size     int64
	file     *os.File
}

// New 打开（或创建）日志文件。backups 为保留的历史份数，0 表示只留当前文件。
func New(dir string, maxBytes int64, backups int) (*Writer, error) {
	if dir == "" {
		return nil, errors.New("日志目录为空")
	}
	if maxBytes <= 0 {
		return nil, errors.New("日志文件上限必须大于 0")
	}
	if backups < 0 {
		backups = 0
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}
	w := &Writer{path: filepath.Join(dir, "app.log"), maxBytes: maxBytes, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// Path 返回当前日志文件的绝对或相对路径。
func (w *Writer) Path() string { return w.path }

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("读取日志文件状态失败: %w", err)
	}
	w.file, w.size = f, fi.Size()
	return nil
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	// 单条日志本身超过上限时也先滚动，避免当前文件无限膨胀
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *Writer) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	if w.backups == 0 {
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return w.open()
	}
	// 丢掉最老一份，其余历史文件依次后移
	oldest := w.path + "." + strconv.Itoa(w.backups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := w.backups - 1; i >= 1; i-- {
		from := w.path + "." + strconv.Itoa(i)
		to := w.path + "." + strconv.Itoa(i+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return w.open()
}

// Close 关闭当前日志文件；重复调用安全。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
