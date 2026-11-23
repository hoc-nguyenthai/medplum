package fhirrouter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// PackageRelease/{id}/$install: nạp một gói định nghĩa. Nội dung gói là Bundle (batch hoặc transaction) nằm trong Binary mà
// PackageRelease.content.url trỏ tới ("Binary/<id>"). Bundle chạy như một batch/transaction thông thường với quyền của
// người gọi, nên mọi kiểm tra quyền và tách project vẫn áp dụng. Mỗi lần gọi ghi một PackageInstallation:
// installing -> installed (mọi entry thành công) hoặc error.
//
// Chỉ admin (nếu Repository cài AdminChecker). Body: không cần.

// AdminChecker là tùy chọn của Repository: trả lỗi (thường là Forbidden) nếu người gọi không phải quản trị.
// Repository không cài thì thao tác quản trị được phép (chế độ không xác thực).
type AdminChecker interface {
	RequireAdmin(ctx context.Context) error
}

// maxPackageBytes là trần mặc định cho nội dung một gói.
const maxPackageBytes = 64 << 20

func (r *Router) registerPackages() {
	r.Add("POST", "PackageRelease/:id/$install", r.packageInstall)
}

func (r *Router) packageInstall(ctx context.Context, req *Request) (*Response, error) {
	if ac, ok := r.repo.(AdminChecker); ok {
		if err := ac.RequireAdmin(ctx); err != nil {
			return nil, err
		}
	}
	repo := r.repoOf(ctx)
	res, err := repo.ReadResource(ctx, "PackageRelease", req.Params["id"])
	if err != nil {
		return nil, err
	}
	pr := res.(*fhir.PackageRelease)
	bundle, err := r.readPackageBundle(ctx, repo, pr)
	if err != nil {
		return nil, err
	}

	inst, err := repo.CreateResource(ctx, &fhir.PackageInstallation{
		ID:             newID(),
		Package:        pr.Package,
		PackageRelease: &fhir.Reference{Reference: "PackageRelease/" + pr.ID},
		Version:        pr.Version,
		Status:         "installing",
	})
	if err != nil {
		return nil, err
	}
	pi := inst.(*fhir.PackageInstallation)

	out, err := r.installBundle(ctx, bundle)
	status := "installed"
	if err != nil {
		status = "error"
	}
	pi.Status = status
	if _, uerr := repo.UpdateResource(ctx, pi, UpdateOptions{}); uerr != nil && err == nil {
		return nil, uerr
	}
	if err != nil {
		return nil, err
	}
	return &Response{Status: 200, Resource: out}, nil
}

// installBundle chạy Bundle của gói (không async) và coi entry có status >= 400 là lỗi của cả lần cài.
func (r *Router) installBundle(ctx context.Context, bundle map[string]any) (*fhir.Bundle, error) {
	resp, err := r.batch(ctx, &Request{Method: "POST", Path: "", Body: bundle})
	if err != nil {
		return nil, err
	}
	out, ok := resp.Resource.(*fhir.Bundle)
	if !ok {
		return nil, BadRequest("Kết quả cài gói không hợp lệ")
	}
	for i, e := range out.Entry {
		if e.Response == nil {
			continue
		}
		code, _ := strconv.Atoi(strings.Fields(e.Response.Status + " ")[0])
		if code >= 400 {
			if oc, ok := anyValue(e.Resource).(*fhir.OperationOutcome); ok && oc != nil {
				return nil, &Error{Status: 400, Outcome: withEntryContext(oc, i)}
			}
			return nil, BadRequest("Entry " + strconv.Itoa(i) + " lỗi: " + e.Response.Status)
		}
	}
	return out, nil
}

func (r *Router) readPackageBundle(ctx context.Context, repo Repository, pr *fhir.PackageRelease) (map[string]any, error) {
	if pr.Content == nil || !strings.HasPrefix(pr.Content.URL, "Binary/") || strings.Contains(pr.Content.URL[len("Binary/"):], "/") {
		return nil, BadRequest("PackageRelease.content.url phải có dạng Binary/<id>")
	}
	res, err := repo.ReadResource(ctx, "Binary", strings.TrimPrefix(pr.Content.URL, "Binary/"))
	if err != nil {
		return nil, err
	}
	b := res.(*fhir.Binary)
	var raw []byte
	if r.opts.BinaryContent != nil {
		raw, err = r.opts.BinaryContent(ctx, b)
	} else if raw, err = base64.StdEncoding.DecodeString(b.Data); err != nil {
		err = BadRequest("Nội dung gói không phải base64 hợp lệ")
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPackageBytes {
		return nil, BadRequest("Gói vượt quá giới hạn " + strconv.Itoa(maxPackageBytes>>20) + " MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var bundle map[string]any
	if err := dec.Decode(&bundle); err != nil || bundle["resourceType"] != "Bundle" {
		return nil, BadRequest("Nội dung gói phải là Bundle JSON")
	}
	return bundle, nil
}

func anyValue(a *fhir.AnyResource) fhir.Resource {
	if a == nil {
		return nil
	}
	return a.Value
}
