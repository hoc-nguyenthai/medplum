// Package httpapi là tầng presentation của fhir-notifier: chỉ các probe sức khỏe. Service không có route nghiệp vụ: việc giao được kích hoạt bởi sự kiện
// trên broker Redis, không phải bởi yêu cầu HTTP; cổng chỉ phục vụ orchestrator và HEALTHCHECK của Docker.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"

	"trivita-fhir/pkg/httpsec"
)

// Deps là mọi thứ presentation cần; được lắp ở bootstrap.
type Deps struct {
	// Ready kiểm các phụ thuộc chính (database, Redis) và trả kết quả từng phụ thuộc; nil: luôn sẵn sàng.
	Ready func(ctx context.Context) (ready bool, checks map[string]bool)
}

// HealthResponse là body của các probe còn sống.
type HealthResponse struct {
	Status string `json:"status"` // luôn "ok" khi tiến trình còn sống
}

// ReadyResponse là body của /health/ready; Checks là kết quả từng phụ thuộc (true = sẵn sàng).
type ReadyResponse struct {
	Ready  bool            `json:"ready"`  // true khi nhận được việc (không đang tắt, database và Redis kết nối được)
	Checks map[string]bool `json:"checks"` // kết quả từng phụ thuộc: database, broker, hoặc draining khi đang tắt
}

// New dựng handler với các probe, header bảo mật và chặn panic.
func New(dep Deps) http.Handler {
	mux := http.NewServeMux()
	live := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
	}
	mux.HandleFunc("GET /healthz", live) // HEALTHCHECK của Docker (pkg/httpserve.HealthCheck)
	mux.HandleFunc("GET /health/live", live)
	mux.HandleFunc("GET /health/startup", live)
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if dep.Ready == nil {
			writeJSON(w, http.StatusOK, ReadyResponse{Ready: true, Checks: map[string]bool{}})
			return
		}
		ready, checks := dep.Ready(r.Context())
		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, ReadyResponse{Ready: ready, Checks: checks})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not Found"})
	})
	return recoverer(httpsec.SecurityHeaders(httpsec.HeadersOptions{})(mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// recoverer biến panic thành 500 không lộ chi tiết (chi tiết vào log).
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic trong handler probe", "panic", v, "stack", string(debug.Stack()))
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": http.StatusText(http.StatusInternalServerError)})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
