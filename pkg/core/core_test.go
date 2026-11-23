package core_test

import (
	"fmt"
	"testing"

	"trivita-fhir/pkg/core"
)

type created struct {
	core.BaseEvent
}

func (created) EventType() string { return "Created" }

func TestNewIDIsUniqueHex(t *testing.T) {
	a, b := core.NewID(), core.NewID()
	if len(a) != 32 || a == b {
		t.Fatalf("id không hợp lệ: %q %q", a, b)
	}
}

func TestAggregateRecordsAndPullsEvents(t *testing.T) {
	var agg core.Aggregate
	agg.Record(created{core.NewBaseEvent()})
	events := agg.PullEvents()
	if len(events) != 1 || events[0].EventType() != "Created" || events[0].EventID() == "" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].OccurredAtTime().IsZero() {
		t.Fatal("thiếu occurred_at")
	}
	if got := agg.PullEvents(); len(got) != 0 {
		t.Fatalf("PullEvents phải xóa event, còn %d", len(got))
	}
}

func TestErrorKinds(t *testing.T) {
	cases := []struct {
		err  error
		kind core.Kind
		name string
	}{
		{core.Business("x"), core.KindBusiness, "business"},
		{core.NotFound("x"), core.KindNotFound, "not_found"},
		{core.Conflict("x"), core.KindConflict, "conflict"},
		{core.Invalid("x %d", 1), core.KindValidation, "validation"},
	}
	for _, c := range cases {
		wrapped := fmt.Errorf("bọc: %w", c.err)
		if !core.IsKind(wrapped, c.kind) || c.kind.String() != c.name {
			t.Errorf("%v: kind sai", c.err)
		}
		if de, ok := core.AsError(wrapped); !ok || de.Error() == "" {
			t.Errorf("%v: AsError", c.err)
		}
	}
	if core.IsKind(fmt.Errorf("lạ"), core.KindBusiness) {
		t.Fatal("lỗi thường không được coi là lỗi nghiệp vụ")
	}
}
