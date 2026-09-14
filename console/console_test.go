package console

import (
	"context"
	"log/slog"
	"testing"
)

// TestDebugDisabledByDefault asserts that an unset REGTOOL_DEBUG leaves debug
// output off and, critically, does not panic.
func TestDebugDisabledByDefault(t *testing.T) {
	t.Setenv(DebugEnvVar, "")
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug should be disabled when REGTOOL_DEBUG is unset")
	}
	Debug("this should not be printed")
}

func TestDebugEnabledByEnvVar(t *testing.T) {
	prev := debugLevel.Level()
	t.Cleanup(func() { debugLevel.Set(prev) })

	debugLevel.Set(slog.LevelDebug)
	if !logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug should be enabled at slog.LevelDebug")
	}
	Debug("this should be printed")
}

func TestPrintHelpersDoNotPanic(t *testing.T) {
	Success("ok")
	Error("err")
	Warning("warn")
	Info("info")
	Println(Color.Cyan, "a", "b")
	Print(Color.White, "c")
	Printf(Color.Red, "%s\n", "d")
}
