package fhirbinary

import (
	"context"
	"errors"
	"net/http"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// PresignConfig cấu hình PresignedURL.
//   - Repo: repository của người gọi (đã tách project và quyền): đọc Binary theo quyền đọc, gồm cả securityContext.
//   - CanUpdate: người gọi có được cập nhật Binary không (cần cho upload=true); nil thì không ai được xin URL tải lên.
type PresignConfig struct {
	Repo      fhirrouter.Repository
	Signer    *binarystore.Signer
	CanUpdate func(ctx context.Context, resourceType string) bool
}

// PresignedURL là handler của `Binary/:id/$presigned-url[?upload=true]`: trả Parameters{url} với URL ký GET (đọc), hoặc PUT (tải lên,
// cần quyền cập nhật) cho phiên bản hiện tại. Binary không đọc được (khác project, thiếu quyền, securityContext) trả 404 hoặc 403 như
// khi đọc.
func PresignedURL(c PresignConfig) fhirrouter.Handler {
	return func(ctx context.Context, req *fhirrouter.Request) (*fhirrouter.Response, error) {
		in, err := fhirrouter.ParseInput(req)
		if err != nil {
			return nil, err
		}
		upload := false
		switch in.String("upload") {
		case "", "false":
		case "true":
			upload = true
		default:
			return nil, fhirrouter.BadRequest("upload phải là true hoặc false")
		}
		res, err := c.Repo.ReadResource(ctx, "Binary", req.Params["id"])
		if err != nil {
			return nil, err
		}
		b, ok := res.(*fhir.Binary)
		if !ok {
			return nil, fhirrouter.NotFound()
		}
		if !canReadSecurityContext(ctx, c.Repo, b) {
			return nil, fhirrouter.Forbidden("Không có quyền đọc ngữ cảnh bảo mật của Binary")
		}
		method := http.MethodGet
		if upload {
			if c.CanUpdate == nil || !c.CanUpdate(ctx, "Binary") {
				return nil, fhirrouter.Forbidden("Không có quyền cập nhật Binary")
			}
			method = http.MethodPut
		}
		project := ""
		if b.Meta != nil {
			project = b.Meta.Project
		}
		u, err := c.Signer.Sign(method, b.ID, versionOf(b), project)
		if err != nil {
			if errors.Is(err, binarystore.ErrNoSigningKey) {
				return nil, fhirrouter.Internal("Storage signing key is not configured")
			}
			return nil, fhirrouter.Internal("Failed to sign URL")
		}
		return &fhirrouter.Response{Status: http.StatusOK, Resource: &fhir.Parameters{Parameter: []fhir.ParametersParameter{{Name: "url", ValueUri: u}}}}, nil
	}
}
