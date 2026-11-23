package temporal

import (
	"crypto/tls"
	"log/slog"

	enumspb "go.temporal.io/api/enums/v1"
)

const (
	enumReuseReject         = enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	enumConflictUseExisting = enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING
)

func tlsConfig() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }

// slogAdapter đưa log của Temporal SDK vào slog (JSON/OTLP của service) thay vì logger mặc định của SDK.
type slogAdapter struct{}

func (slogAdapter) Debug(msg string, kv ...any) { slog.Debug(msg, kv...) }
func (slogAdapter) Info(msg string, kv ...any)  { slog.Info(msg, kv...) }
func (slogAdapter) Warn(msg string, kv ...any)  { slog.Warn(msg, kv...) }
func (slogAdapter) Error(msg string, kv ...any) { slog.Error(msg, kv...) }
