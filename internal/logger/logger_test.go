package logger

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestLogger(t *testing.T) {
	var buf bytes.Buffer
	InitWithWriter(&buf)

	Warn("warn msg", "warn detail")

	t.Log(buf.String())
	if !bytes.Contains(buf.Bytes(), []byte("warn msg")) {
		t.Errorf("日志内容未包含预期消息")
	}

	// 切换到 debug 级别
	SetLevel(slog.LevelDebug)
	Debug("debug msg", "debug detail")

	t.Log(buf.String())
	if !bytes.Contains(buf.Bytes(), []byte("debug msg")) {
		t.Errorf("日志内容未包含 debug 消息")
	}
}
