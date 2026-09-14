// Package console provides small colored terminal output helpers.
//
// Debug output is off by default and is enabled by setting the environment
// variable REGTOOL_DEBUG to any non-empty value.
package console

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// DebugEnvVar is the environment variable that enables Debug output when set
// to any non-empty value.
const DebugEnvVar = "REGTOOL_DEBUG"

var Color = struct {
	Reset  string
	Red    string
	Green  string
	Yellow string
	Blue   string
	Purple string
	Cyan   string
	White  string
}{
	Reset:  "\033[0m",
	Red:    "\033[31m",
	Green:  "\033[32m",
	Yellow: "\033[33m",
	Blue:   "\033[34m",
	Purple: "\033[35m",
	Cyan:   "\033[36m",
	White:  "\033[37m",
}

// debugLevel is the level of the package logger. It is set to slog.LevelDebug
// only when REGTOOL_DEBUG is a non-empty value; otherwise debug records are
// dropped.
var debugLevel = new(slog.LevelVar)

// logger writes debug records to stderr. A missing REGTOOL_DEBUG simply leaves
// the logger at info level, so nothing is logged and nothing panics.
var logger *slog.Logger

func init() {
	if os.Getenv(DebugEnvVar) != "" {
		debugLevel.Set(slog.LevelDebug)
	} else {
		debugLevel.Set(slog.LevelInfo)
	}
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: debugLevel}))
}

func Println(color string, messages ...string) {
	message := strings.Join(messages, " ")
	fmt.Println(color + message + Color.Reset)
}

func Print(color string, messages ...string) {
	message := strings.Join(messages, " ")
	fmt.Print(color + message + Color.Reset)
}

func Printf(color string, format string, a ...interface{}) {
	fmt.Printf(color+format+Color.Reset, a...)
}

func Success(messages ...string) {
	Println(Color.Green, messages...)
}

func Error(messages ...string) {
	Println(Color.Red, messages...)
}

func Warning(messages ...string) {
	Println(Color.Yellow, messages...)
}

func Info(messages ...string) {
	Println(Color.Blue, messages...)
}

// Debug prints the message only when REGTOOL_DEBUG is set to a non-empty
// value.
func Debug(messages ...string) {
	if !logger.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	Println(Color.Purple, messages...)
}
