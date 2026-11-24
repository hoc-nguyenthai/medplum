package fhirrouter_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trivita-fhir/pkg/fhirrouter"
)

// AuthorizeEntry kiểm quyền từng entry của Bundle: entry bị từ chối không được chạy (không tác dụng phụ), và cách từ chối khác nhau theo loại:
// batch đồng bộ từ chối riêng entry, transaction và batch async từ chối cả Bundle.

func denyReindex(_ context.Context, method, path string) error {
	if strings.Contains(path, "$reindex") || strings.Contains(path, "$expunge") {
		return fhirrouter.Forbidden("Token không đủ quyền cho thao tác này")
	}
	return nil
}

const mixedBatch = `{"resourceType":"Bundle","type":"%s","entry":[
 {"request":{"method":"POST","url":"Patient"},"resource":{"resourceType":"Patient","name":[{"family":"Ok"}]}},
 {"request":{"method":"POST","url":"Patient/$reindex"}},
 {"request":{"method":"POST","url":"Patient"},"resource":{"resourceType":"Patient","name":[{"family":"Ok2"}]}}
]}`

func TestAuthorizeEntryBatchDeniesOnlyThatEntry(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{AuthorizeEntry: denyReindex})
	code, body := putTo(r.HTTPHandler(), "POST", "", strings.Replace(mixedBatch, "%s", "batch", 1))
	if code != 200 {
		t.Fatalf("batch: %d %s", code, body)
	}
	if !strings.Contains(body, `"status":"403 Forbidden"`) || strings.Count(body, `"status":"201 Created"`) != 2 {
		t.Errorf("muốn entry 1 là 403 và hai entry còn lại 201: %s", body)
	}
	if n := count(t, repo, "Patient", ""); n != 2 {
		t.Errorf("số Patient = %d, muốn 2 (các entry hợp lệ vẫn chạy)", n)
	}
}

func TestAuthorizeEntryTransactionRejectsWholeBundle(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{AuthorizeEntry: denyReindex})
	code, body := putTo(r.HTTPHandler(), "POST", "", strings.Replace(mixedBatch, "%s", "transaction", 1))
	if code != 403 {
		t.Fatalf("transaction: %d %s, muốn 403", code, body)
	}
	if !strings.Contains(body, "Bundle.entry[1]") {
		t.Errorf("lỗi phải nêu entry bị từ chối: %s", body)
	}
	if n := count(t, repo, "Patient", ""); n != 0 {
		t.Errorf("số Patient = %d, muốn 0 (không entry nào chạy)", n)
	}
}

func TestAuthorizeEntryAsyncBatchRejectsWholeBundleAndCreatesNoJob(t *testing.T) {
	repo := fhirrouter.NewMemoryRepository()
	r := newRouter(t, repo, fhirrouter.Options{AsyncBatch: true, AuthorizeEntry: denyReindex})
	req := httptestRequest("POST", "/", strings.Replace(mixedBatch, "%s", "batch", 1), "Prefer", "respond-async")
	w := serve(r.HTTPHandler(), req)
	if w.StatusCode != 403 {
		t.Fatalf("batch async: %d, muốn 403", w.StatusCode)
	}
	if n := count(t, repo, "AsyncJob", ""); n != 0 {
		t.Errorf("số AsyncJob = %d, muốn 0 (chặn trước khi tạo job)", n)
	}
	if n := count(t, repo, "Patient", ""); n != 0 {
		t.Errorf("số Patient = %d, muốn 0", n)
	}
}

func TestAuthorizeEntryReceivesMethodAndRelativePath(t *testing.T) {
	var got []string
	spy := func(_ context.Context, method, path string) error {
		got = append(got, method+" "+path)
		if method == "DELETE" {
			return errors.New("lỗi thường")
		}
		return nil
	}
	r := newRouter(t, fhirrouter.NewMemoryRepository(), fhirrouter.Options{AuthorizeEntry: spy})
	b := `{"resourceType":"Bundle","type":"batch","entry":[
 {"request":{"method":"get","url":"Patient/x?_summary=count"}},
 {"request":{"method":"DELETE","url":"/Patient/y"}}]}`
	code, body := putTo(r.HTTPHandler(), "POST", "", b)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if strings.Join(got, "|") != "GET Patient/x|DELETE Patient/y" {
		t.Errorf("hook nhận %v, muốn method viết hoa và đường dẫn bỏ query và dấu / đầu", got)
	}
	if !strings.Contains(body, `"status":"500 Internal Server Error"`) {
		t.Errorf("lỗi không phải *Error phải thành 500 của entry, không lộ chi tiết: %s", body)
	}
}
