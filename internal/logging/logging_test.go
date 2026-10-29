package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeN(t *testing.T, w *Writer, n int, ch string) {
	t.Helper()
	if _, err := w.Write([]byte(strings.Repeat(ch, n))); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(data)
}

func TestWriterRotates(t *testing.T) {
	w, err := New(t.TempDir(), 100, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	writeN(t, w, 60, "a")
	writeN(t, w, 60, "b")
	if got := readFile(t, w.path+".1"); got != strings.Repeat("a", 60) {
		t.Errorf("历史文件内容错误: %q", got)
	}
	if got := readFile(t, w.path); got != strings.Repeat("b", 60) {
		t.Errorf("当前文件应只含新内容: %q", got)
	}
}

// 重启后按已有文件大小继续计数，不会因为重新打开而无限增长。
func TestWriterResumesSize(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, 100, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	writeN(t, w, 60, "a")
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again, err := New(dir, 100, 1)
	if err != nil {
		t.Fatalf("重新打开: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	writeN(t, again, 60, "b")
	if got := readFile(t, again.path+".1"); got != strings.Repeat("a", 60) {
		t.Errorf("重启后未按已有大小滚动: %q", got)
	}
	if got := readFile(t, again.path); got != strings.Repeat("b", 60) {
		t.Errorf("当前文件内容错误: %q", got)
	}
}

// 历史份数超过 backups 时丢弃最老的一份。
func TestWriterKeepsBackups(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, 10, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	for _, ch := range []string{"a", "b", "c", "d"} {
		writeN(t, w, 10, ch)
	}
	for _, name := range []string{"app.log.1", "app.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s 应存在: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "app.log.3")); !os.IsNotExist(err) {
		t.Errorf("超出份数的 app.log.3 不应存在: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "app.log")); got != strings.Repeat("d", 10) {
		t.Errorf("当前文件内容错误: %q", got)
	}
}

func TestNewInvalidArgs(t *testing.T) {
	if _, err := New("", 100, 1); err == nil {
		t.Error("空目录应报错")
	}
	if _, err := New(t.TempDir(), 0, 1); err == nil {
		t.Error("非法上限应报错")
	}
}
