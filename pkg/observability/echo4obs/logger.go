package echo4obs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

// Logger adapts a *slog.Logger to Echo v4's echo.Logger, so messages from Echo and its
// middlewares (Recover, ...) get the same JSON format and OTLP export as the application.
// Output, prefix and header settings are ignored: the slog handler owns the format.
type Logger struct {
	l      *slog.Logger
	prefix string
	level  log.Lvl
}

var _ echo.Logger = (*Logger)(nil)

// NewLogger returns an echo.Logger writing to l.
func NewLogger(l *slog.Logger) *Logger { return &Logger{l: l, level: log.DEBUG} }

func (g *Logger) Output() io.Writer         { return os.Stdout }
func (g *Logger) SetOutput(io.Writer)       {}
func (g *Logger) Prefix() string            { return g.prefix }
func (g *Logger) SetPrefix(p string)        { g.prefix = p }
func (g *Logger) Level() log.Lvl            { return g.level }
func (g *Logger) SetLevel(v log.Lvl)        { g.level = v }
func (g *Logger) SetHeader(string)          {}
func (g *Logger) Print(i ...any)            { g.log(log.INFO, fmt.Sprint(i...)) }
func (g *Logger) Printf(f string, a ...any) { g.log(log.INFO, fmt.Sprintf(f, a...)) }
func (g *Logger) Printj(j log.JSON)         { g.logj(log.INFO, j) }
func (g *Logger) Debug(i ...any)            { g.log(log.DEBUG, fmt.Sprint(i...)) }
func (g *Logger) Debugf(f string, a ...any) { g.log(log.DEBUG, fmt.Sprintf(f, a...)) }
func (g *Logger) Debugj(j log.JSON)         { g.logj(log.DEBUG, j) }
func (g *Logger) Info(i ...any)             { g.log(log.INFO, fmt.Sprint(i...)) }
func (g *Logger) Infof(f string, a ...any)  { g.log(log.INFO, fmt.Sprintf(f, a...)) }
func (g *Logger) Infoj(j log.JSON)          { g.logj(log.INFO, j) }
func (g *Logger) Warn(i ...any)             { g.log(log.WARN, fmt.Sprint(i...)) }
func (g *Logger) Warnf(f string, a ...any)  { g.log(log.WARN, fmt.Sprintf(f, a...)) }
func (g *Logger) Warnj(j log.JSON)          { g.logj(log.WARN, j) }
func (g *Logger) Error(i ...any)            { g.log(log.ERROR, fmt.Sprint(i...)) }
func (g *Logger) Errorf(f string, a ...any) { g.log(log.ERROR, fmt.Sprintf(f, a...)) }
func (g *Logger) Errorj(j log.JSON)         { g.logj(log.ERROR, j) }

func (g *Logger) Fatal(i ...any) {
	g.log(log.ERROR, fmt.Sprint(i...))
	os.Exit(1)
}

func (g *Logger) Fatalf(f string, a ...any) {
	g.log(log.ERROR, fmt.Sprintf(f, a...))
	os.Exit(1)
}

func (g *Logger) Fatalj(j log.JSON) {
	g.logj(log.ERROR, j)
	os.Exit(1)
}

func (g *Logger) Panic(i ...any) {
	msg := fmt.Sprint(i...)
	g.log(log.ERROR, msg)
	panic(msg)
}

func (g *Logger) Panicf(f string, a ...any) {
	msg := fmt.Sprintf(f, a...)
	g.log(log.ERROR, msg)
	panic(msg)
}

func (g *Logger) Panicj(j log.JSON) {
	g.logj(log.ERROR, j)
	panic(fmt.Sprint(j))
}

func (g *Logger) log(level log.Lvl, msg string, args ...any) {
	if level < g.level {
		return
	}
	g.l.Log(context.Background(), slogLevel(level), g.prefix+msg, args...)
}

func (g *Logger) logj(level log.Lvl, j log.JSON) {
	args := make([]any, 0, 2*len(j))
	for k, v := range j {
		args = append(args, k, v)
	}
	g.log(level, "", args...)
}

func slogLevel(l log.Lvl) slog.Level {
	switch l {
	case log.DEBUG:
		return slog.LevelDebug
	case log.WARN:
		return slog.LevelWarn
	case log.ERROR:
		return slog.LevelError
	}
	return slog.LevelInfo
}
