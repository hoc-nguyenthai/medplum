package bootstrap

import (
	"context"
	"errors"
	"strings"

	"trivita-fhir/apps/ws-gateway/internal/presentation/httpapi"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhircast"
	fcws "trivita-fhir/pkg/fhircast/ws"
)

// claimsVerifier là phần của fhirauth.Verifier mà Hub cần (để test không cần dựng JWT thật).
type claimsVerifier interface {
	VerifyClaims(ctx context.Context, raw string) (*fhirauth.Claims, error)
}

// authenticator đổi Verifier thành hàm xác thực của presentation: token sai là 401; token đúng mà không có project, hoặc
// (khi requireScope) không có scope fhircast/*, là 403. Không cần quyền fhir.* vì Hub không đụng tới dữ liệu FHIR.
func authenticator(v claimsVerifier, requireScope bool) func(context.Context, string) (httpapi.Caller, error) {
	return func(ctx context.Context, raw string) (httpapi.Caller, error) {
		cl, err := v.VerifyClaims(ctx, raw)
		if err != nil {
			if errors.Is(err, fhirauth.ErrInvalidToken) {
				return httpapi.Caller{}, httpapi.ErrUnauthorized
			}
			return httpapi.Caller{}, err
		}
		if cl.Project == "" { // không có project thì không có cách tách dữ liệu giữa các tenant
			return httpapi.Caller{}, httpapi.ErrForbidden
		}
		if requireScope && !hasFhircastScope(cl.Scopes) {
			return httpapi.Caller{}, httpapi.ErrForbidden
		}
		return httpapi.Caller{Subject: cl.Subject, Project: cl.Project}, nil
	}
}

func hasFhircastScope(scopes []string) bool {
	for _, s := range scopes {
		if strings.HasPrefix(s, "fhircast/") {
			return true
		}
	}
	return false
}

// buildFhircast dựng Hub FHIRcast trên Redis dùng chung (Store + Bus) và trả deps cho presentation.
func (c *Container) buildFhircast() (*httpapi.FhircastDeps, error) {
	s := c.Settings
	v, err := fhirauth.NewVerifier(fhirauth.Config{
		Issuer: s.FhircastJWTIssuer, Audience: s.FhircastJWTAudience, JWKSURL: s.FhircastJWKSURL, HMACSecret: []byte(s.FhircastJWTHMACSecret),
	})
	if err != nil {
		return nil, err
	}
	client := c.Redis.Client()
	store := fhircast.NewRedisStore(client, "", fhircast.StoreOptions{
		Lease: s.FhircastLease, ContextTTL: s.FhircastContextTTL, MaxContextBytes: s.FhircastMaxContextBytes,
	})
	bus := fhircast.NewRedisBus(client)
	// Bus mượn client của messaging.Redis nên phải đóng trước Redis.Stop (closers chạy ngược thứ tự đăng ký).
	c.onStop(func(context.Context) error { return bus.Close() })
	sockets, err := fcws.New(fcws.Config{
		Store: store, Bus: bus,
		MaxConns: s.FhircastMaxConnections, MaxConnsPerIP: s.WSMaxConnsPerIP, MaxSocketsPerEndpoint: s.FhircastMaxSocketsPerEndpoint,
		ClientIP: c.clientIP, OriginPatterns: s.OriginPatterns(), CloseTimeout: s.WSCloseTimeout, Heartbeat: s.FhircastHeartbeat,
	})
	if err != nil {
		return nil, err
	}
	c.FhircastWS = sockets
	// Đóng socket (1001) TRƯỚC khi đóng bus: đăng ký sau chạy trước.
	c.onStop(sockets.Close)
	c.Fhircast = &fhircast.Hub{Store: store, Bus: bus, Lease: s.FhircastLease, MaxUpdateEntries: s.FhircastMaxUpdateEntries}
	return &httpapi.FhircastDeps{
		Hub: c.Fhircast, Authenticate: authenticator(v, s.FhircastRequireScope), WSBase: s.FhircastPublicURL,
		MaxBody: s.FhircastMaxBodyBytes, AllowTopicMismatch: s.FhircastAllowTopicMismatch, WebSocket: sockets.Handler(),
	}, nil
}
