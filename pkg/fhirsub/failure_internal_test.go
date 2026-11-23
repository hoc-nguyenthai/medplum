package fhirsub

import (
	"context"
	"testing"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
)

// autoDisable không ghi đè Subscription đã bị đổi trạng thái (tương đương thao tác test /status = active của Medplum).
func TestAutoDisableKeepsChangedStatus(t *testing.T) {
	ctx := context.Background()
	mem := fhirrouter.NewMemoryRepository()
	m := NewManager(mem, Config{AutoDisable: []Trigger{{MaxConsecutiveFailures: 1, TimeWindowSeconds: 60}}})
	defer m.Stop(ctx)
	created, err := mem.CreateResource(ctx, &fhir.Subscription{ID: "s", Status: "error", Criteria: "Observation",
		Channel: &fhir.SubscriptionChannel{Type: "rest-hook", Endpoint: "https://example.org/hook"}})
	if err != nil {
		t.Fatal(err)
	}
	m.autoDisable("s", m.cfg.AutoDisable[0], 1)
	r, _ := mem.ReadResource(ctx, "Subscription", "s")
	if s := r.(*fhir.Subscription); s.Status != "error" || s.Error != "" || s.Meta.VersionId != created.GetMeta().VersionId {
		t.Errorf("Subscription bị ghi đè: status=%s error=%q version=%s", s.Status, s.Error, s.Meta.VersionId)
	}
}
