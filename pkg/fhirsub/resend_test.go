package fhirsub_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"trivita-fhir/pkg/core"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsub"
)

type capture struct {
	mu     sync.Mutex
	events []fhirsub.ChangeEvent
}

func (c *capture) Publish(_ context.Context, e core.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e.(fhirsub.ChangeEvent))
	return nil
}

func (c *capture) last() fhirsub.ChangeEvent { return c.events[len(c.events)-1] }

func status(err error) int {
	var fe *fhirrouter.Error
	if errors.As(err, &fe) {
		return fe.Status
	}
	return 0
}

// ResendSubscriptions phát lại sự kiện ghi mà không ghi gì vào kho: bản hiện tại, một phiên bản lịch sử, và phiên bản cuối trước lần xóa.
func TestResendSubscriptions(t *testing.T) {
	ctx := context.Background()
	base := fhirrouter.NewMemoryRepository()
	pub := &capture{}
	repo := fhirsub.Wrap(base, pub)
	created, err := repo.CreateResource(ctx, &fhir.Patient{ID: "p1", Gender: "male"})
	if err != nil {
		t.Fatal(err)
	}
	v1 := created.GetMeta().VersionId
	upd := &fhir.Patient{ID: "p1", Gender: "female"}
	if _, err := repo.UpdateResource(ctx, upd, fhirrouter.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	pub.events = nil

	if err := repo.ResendSubscriptions(ctx, "Patient", "p1", "", "update"); err != nil {
		t.Fatal(err)
	}
	if len(pub.events) != 1 || pub.last().Interaction != "update" || pub.last().ID != "p1" || pub.last().ResourceType != "Patient" || !contains(pub.last().Resource, `"female"`) {
		t.Fatalf("bản hiện tại: %+v", pub.events)
	}
	// phiên bản lịch sử và interaction create
	if err := repo.ResendSubscriptions(ctx, "Patient", "p1", v1, "create"); err != nil {
		t.Fatal(err)
	}
	if len(pub.events) != 2 || pub.last().Interaction != "create" || pub.last().VersionID != v1 || !contains(pub.last().Resource, `"male"`) {
		t.Fatalf("phiên bản cũ: %+v", pub.last())
	}
	// không ghi gì: số phiên bản không đổi
	hist, _ := base.ReadHistory(ctx, "Patient", "p1")
	if len(hist) != 2 {
		t.Errorf("resend đã ghi vào kho: %d phiên bản", len(hist))
	}
	// resource hoặc phiên bản không có
	if err := repo.ResendSubscriptions(ctx, "Patient", "none", "", "update"); status(err) != http.StatusNotFound {
		t.Errorf("resource không có: %v", err)
	}
	if err := repo.ResendSubscriptions(ctx, "Patient", "p1", "khong-co", "update"); status(err) != http.StatusNotFound {
		t.Errorf("phiên bản không có: %v", err)
	}
	// delete: chỉ khi đã bị xóa, phát lại phiên bản cuối trước lần xóa
	if err := repo.ResendSubscriptions(ctx, "Patient", "p1", "", "delete"); status(err) != http.StatusPreconditionFailed {
		t.Errorf("chưa xóa mà gửi lại sự kiện xóa: %v", err)
	}
	if err := repo.DeleteResource(ctx, "Patient", "p1"); err != nil {
		t.Fatal(err)
	}
	pub.events = nil
	if err := repo.ResendSubscriptions(ctx, "Patient", "p1", "", "delete"); err != nil {
		t.Fatal(err)
	}
	if len(pub.events) != 1 || pub.last().Interaction != "delete" || !contains(pub.last().Resource, `"female"`) {
		t.Errorf("sự kiện xóa: %+v", pub.events)
	}
	if err := repo.ResendSubscriptions(ctx, "Patient", "p1", "sai-phien-ban", "delete"); status(err) != http.StatusBadRequest {
		t.Errorf("versionId sai khi gửi lại sự kiện xóa: %v", err)
	}
}

func contains(raw []byte, sub string) bool {
	return len(raw) > 0 && string(raw) != "" && stringContains(string(raw), sub)
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
