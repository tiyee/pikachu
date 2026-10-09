package log

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gopkg.in/natefinch/lumberjack.v2"
	"pikachu/internal/types"
)

func TestJSONCreatesDirectoriesAndRotates(t *testing.T) {
	d := filepath.Join(t.TempDir(), "nested", "logs")
	if err := Init(&types.LogConfig{Format: "json", Directory: d, MaxSize: 1, MaxBackups: 2, MaxAge: 1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	Info("before rotation")
	if err := logger.closers[0].(*lumberjack.Logger).Rotate(); err != nil {
		t.Fatal(err)
	}
	Info("after rotation")
	Close()
	Close()
	data, err := os.ReadFile(filepath.Join(d, "output.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "after rotation") || strings.Contains(string(data), "before rotation") {
		t.Fatal("rotation did not split logs")
	}
	files, err := filepath.Glob(filepath.Join(d, "output-*.log*"))
	if err != nil || len(files) < 1 {
		t.Fatalf("backup files=%v err=%v", files, err)
	}
}

func TestInitErrorsDoNotPanic(t *testing.T) {
	d := t.TempDir()
	file := filepath.Join(d, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*types.LogConfig{nil, {Format: "invalid"}, {Format: "json", Directory: file}, {Format: "text", Level: "invalid"}} {
		if err := Init(c); err == nil {
			t.Fatal("invalid logger configuration accepted")
		}
	}
	if GetLogger() == nil {
		t.Fatal("logger unavailable")
	}
}

func TestSlogGroupsAndResolvedValues(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	h := NewZapSlogAdapter(zap.New(core))
	if h.WithGroup("") != h {
		t.Fatal("empty group must be ignored")
	}
	l := slog.New(h).With("before", "x").WithGroup("outer").With("inside", "y").WithGroup("inner")
	l.Info("event", slog.Group("detail", slog.Int("answer", 42)), slog.Group("", slog.String("flat", "ok")), slog.Group("empty"), slog.Any("resolved", slogValue{}))
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatal("missing log")
	}
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	for _, field := range entries[0].Context {
		field.AddTo(encoder)
	}
	encoded, err := encoder.EncodeEntry(zapcore.Entry{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoded.Free()
	var data map[string]interface{}
	if err := json.Unmarshal(encoded.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	outer := data["outer"].(map[string]interface{})
	inner := outer["inner"].(map[string]interface{})
	if data["before"] != "x" || outer["inside"] != "y" || inner["flat"] != "ok" || inner["resolved"] != "resolved" || inner["detail"].(map[string]interface{})["answer"] != float64(42) {
		t.Fatalf("incorrect groups: %v", data)
	}
	if _, ok := inner["empty"]; ok {
		t.Fatal("empty group emitted")
	}
}

type slogValue struct{}

func (slogValue) LogValue() slog.Value { return slog.StringValue("resolved") }
