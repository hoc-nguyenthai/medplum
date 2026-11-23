package application

import (
	"context"
	"errors"

	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// consumeSecurityRequest đánh dấu request đã dùng bằng so-sánh-và-đổi theo phiên bản; thua cuộc đua thì "Already used".
func consumeSecurityRequest(ctx context.Context, repo SecurityRequestRepository, r *fhir.UserSecurityRequest) error {
	versionID := ""
	if r.Meta != nil {
		versionID = r.Meta.VersionId
	}
	if err := repo.MarkUsedIfMatch(ctx, r, versionID); err != nil {
		if errors.Is(err, domain.ErrVersionConflict) {
			return domain.BadRequest("Already used")
		}
		return err
	}
	return nil
}

// ConsumeSecurityRequest tiêu thụ một UserSecurityRequest (dùng một lần).
func (s *Service) ConsumeSecurityRequest(ctx context.Context, r *fhir.UserSecurityRequest) error {
	return consumeSecurityRequest(ctx, s.SecurityRequests, r)
}

// retireSecurityRequests đánh dấu used mọi request chưa dùng của user thỏa pred, trong cửa sổ hiệu lực dài nhất. Gom hết
// danh sách trước rồi mới sửa từng cái (MarkUsed chỉ ghi `used` lên bản mới nhất, nên không ghi đè một request đang bị tiêu thụ).
func (s *Service) retireSecurityRequests(ctx context.Context, repo SecurityRequestRepository, user *fhir.User, pred func(*fhir.UserSecurityRequest) bool) error {
	since := s.Clock.Now().Add(-domain.MaxSecurityRequestWindow)
	reqs, err := repo.ListSince(ctx, "User/"+user.ID, since)
	if err != nil {
		return err
	}
	for i := range reqs {
		if !domain.IsTrue(reqs[i].Used) && pred(&reqs[i]) {
			if err := repo.MarkUsed(ctx, reqs[i].ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// SupersedePriorSecurityRequests vô hiệu các request cùng loại chưa dùng của user (trước khi phát request mới).
func (s *Service) SupersedePriorSecurityRequests(ctx context.Context, user *fhir.User, typ string) error {
	return s.retireSecurityRequests(ctx, s.SecurityRequests, user, func(r *fhir.UserSecurityRequest) bool { return r.Type == typ })
}

// RetirePasswordSecurityRequests vô hiệu mọi request mà /auth/setpassword chấp nhận: mọi loại trừ verify-email, kể cả
// request không có loại.
func (s *Service) RetirePasswordSecurityRequests(ctx context.Context, user *fhir.User) error {
	return s.retirePassword(ctx, s.SecurityRequests, user)
}

func (s *Service) retirePassword(ctx context.Context, repo SecurityRequestRepository, user *fhir.User) error {
	return s.retireSecurityRequests(ctx, repo, user, func(r *fhir.UserSecurityRequest) bool { return r.Type != domain.SecurityRequestVerifyEmail })
}

// RetireAllSecurityRequests vô hiệu mọi request chưa dùng của user.
func (s *Service) RetireAllSecurityRequests(ctx context.Context, user *fhir.User) error {
	return s.retireSecurityRequests(ctx, s.SecurityRequests, user, func(*fhir.UserSecurityRequest) bool { return true })
}
