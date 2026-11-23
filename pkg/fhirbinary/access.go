package fhirbinary

import (
	"context"
	"errors"
	"log/slog"
	"regexp"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

var localRef = regexp.MustCompile(`^([A-Za-z]{1,64})/([A-Za-z0-9\-.]{1,64})$`)

// canReadSecurityContext áp quy tắc của Medplum: Binary có securityContext chỉ đọc được khi người gọi đọc được resource mà nó trỏ tới
// (kiểm bằng repository của người gọi). Không có securityContext thì chỉ cần đọc được Binary. Tham chiếu không dạng Type/id (URL, urn,
// tham chiếu lồng nhau) bị coi là không đọc được: mặc định từ chối.
func canReadSecurityContext(ctx context.Context, repo fhirrouter.Repository, b *fhir.Binary) bool {
	if b.SecurityContext == nil || b.SecurityContext.Reference == "" {
		return true
	}
	m := localRef.FindStringSubmatch(b.SecurityContext.Reference)
	if m == nil {
		return false
	}
	if _, err := repo.ReadResource(ctx, m[1], m[2]); err != nil {
		var fe *fhirrouter.Error
		if !errors.As(err, &fe) || (fe.Status != 403 && fe.Status != 404 && fe.Status != 410) {
			slog.WarnContext(ctx, "fhirbinary: không kiểm được securityContext", "binary", "Binary/"+b.ID, "error", err.Error())
		}
		return false
	}
	return true
}
