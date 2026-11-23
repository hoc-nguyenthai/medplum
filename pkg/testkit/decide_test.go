package testkit

import (
	"errors"
	"testing"
)

// Không cần Docker: quyết định chạy, bỏ qua hay thất bại khi thiếu Docker (đặc biệt: CI đặt TRIVITA_REQUIRE_DOCKER=1 thì KHÔNG được bỏ qua).
func TestDecide(t *testing.T) {
	boom := errors.New("không có docker")
	for name, c := range map[string]struct {
		err     error
		require bool
		want    outcome
	}{
		"có Docker":                      {nil, false, outcomeRun},
		"có Docker và đòi Docker":        {nil, true, outcomeRun},
		"thiếu Docker, dev: bỏ qua":      {boom, false, outcomeSkip},
		"thiếu Docker, CI đòi: thất bại": {boom, true, outcomeFail},
	} {
		if got := decide(c.err, c.require); got != c.want {
			t.Errorf("%s: %v, muốn %v", name, got, c.want)
		}
	}
}

func TestEnvDefault(t *testing.T) {
	t.Setenv("TESTKIT_X", "")
	if env("TESTKIT_X", "d") != "d" {
		t.Error("rỗng phải dùng mặc định")
	}
	t.Setenv("TESTKIT_X", "v")
	if env("TESTKIT_X", "d") != "v" {
		t.Error("đặt thì dùng giá trị")
	}
}
