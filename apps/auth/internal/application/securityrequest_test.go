package application_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

func (e *env) secReq(user *fhir.User, typ string) *fhir.UserSecurityRequest {
	return put(e, &fhir.UserSecurityRequest{ResourceType: "UserSecurityRequest", Type: typ, Secret: domain.HashSecurityRequestSecret(domain.GenerateSecret(16)),
		User: &fhir.Reference{Reference: "User/" + user.ID}, ExpiresAt: domain.SecurityRequestExpiration(typ, time.Now())})
}

func (e *env) used(id string) bool {
	e.t.Helper()
	sr, err := e.r.SecurityRequests.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return domain.IsTrue(sr.Used)
}

func TestConsumeSecurityRequest(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		sr := e.secReq(u, "reset")
		if err := e.svc.ConsumeSecurityRequest(ctx, sr); err != nil {
			t.Fatal(err)
		}
		if !e.used(sr.ID) {
			t.Error("phải được đánh dấu đã dùng")
		}
		wantErr(t, e.svc.ConsumeSecurityRequest(ctx, sr), domain.KindBadRequest, "Already used")
		// không có versionId: không thể tiêu thụ có điều kiện, từ chối thay vì ghi không điều kiện
		err := e.svc.ConsumeSecurityRequest(ctx, &fhir.UserSecurityRequest{ID: sr.ID})
		wantErr(t, err, domain.KindInternal, "")
	})
}

func TestConsumeSecurityRequestConcurrent(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		sr := e.secReq(e.user("a@example.org", nil), "reset")
		const n = 8
		var ok, already atomic.Int32
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				switch err := e.svc.ConsumeSecurityRequest(ctx, sr); {
				case err == nil:
					ok.Add(1)
				case domain.Is(err, domain.KindBadRequest, "Already used"):
					already.Add(1)
				default:
					t.Errorf("lỗi lạ: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 1 || already.Load() != n-1 {
			t.Errorf("thắng=%d thua=%d", ok.Load(), already.Load())
		}
	})
}

func TestSupersedeAndRetire(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, other := e.user("a@example.org", nil), e.user("b@example.org", nil)

		// Supersede: chỉ cùng loại, chưa dùng, của đúng user
		r1, r2, v := e.secReq(u, "reset"), e.secReq(u, "reset"), e.secReq(u, "verify-email")
		o := e.secReq(other, "reset")
		if err := e.svc.SupersedePriorSecurityRequests(ctx, u, "reset"); err != nil {
			t.Fatal(err)
		}
		if !e.used(r1.ID) || !e.used(r2.ID) || e.used(v.ID) || e.used(o.ID) {
			t.Error("supersede reset: chỉ hai request reset của user bị dùng")
		}

		// RetirePassword: mọi loại trừ verify-email, kể cả không có loại
		reset, invite, verify, untyped := e.secReq(u, "reset"), e.secReq(u, "invite"), e.secReq(u, "verify-email"), e.secReq(u, "")
		if err := e.svc.RetirePasswordSecurityRequests(ctx, u); err != nil {
			t.Fatal(err)
		}
		if !e.used(reset.ID) || !e.used(invite.ID) || !e.used(untyped.ID) || e.used(verify.ID) || e.used(o.ID) {
			t.Error("retire password: mọi loại trừ verify-email")
		}

		// RetireAll
		if err := e.svc.RetireAllSecurityRequests(ctx, u); err != nil {
			t.Fatal(err)
		}
		if !e.used(verify.ID) || !e.used(v.ID) || e.used(o.ID) {
			t.Error("retire all: gồm cả verify-email, không động tới user khác")
		}

		// request đã dùng không bị ghi lại (không tạo phiên bản mới)
		before, _ := e.r.SecurityRequests.Get(ctx, r1.ID)
		_ = e.svc.RetireAllSecurityRequests(ctx, u)
		after, _ := e.r.SecurityRequests.Get(ctx, r1.ID)
		if before.Meta.VersionId != after.Meta.VersionId {
			t.Error("request đã dùng không được ghi lại")
		}
	})
}

func TestRetireOnlyWithinMaxWindow(t *testing.T) {
	eachEnv(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u := e.user("a@example.org", nil)
		old := e.secReq(u, "reset")
		e.clock.t = time.Now().Add(domain.MaxSecurityRequestWindow + 24*time.Hour) // request đã quá cửa sổ dài nhất
		if err := e.svc.RetireAllSecurityRequests(ctx, u); err != nil {
			t.Fatal(err)
		}
		if e.used(old.ID) {
			t.Error("request cũ hơn cửa sổ dài nhất không bị động tới (đằng nào cũng đã hết hạn)")
		}
	})
}
