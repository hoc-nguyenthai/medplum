package bootstrap_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/fhir-server/internal/bootstrap"
	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/fhirbinary"
)

func TestSweepBinaryCommand(t *testing.T) {
	dir := t.TempDir()
	mut, _ := storageSettings(t)
	s := settings(mut, func(s *config.Settings) {
		s.DatabaseURL = "sqlite://" + dir + "/fhir.db"
	})
	_, srv := newServer(t, s)
	up := want(t, rawCall(t, srv, "POST", "/fhir/R4/Binary", writeSecret, map[string]string{"Content-Type": "text/plain"}, []byte("còn dùng")), 201)
	id := up.str(t, "id")

	blobs := s.StorageDir()
	plant := func(id string, age time.Duration) string {
		p := filepath.Join(blobs, id, "v1")
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("mồ côi"), 0o640); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-age)
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old, young := plant("ghost-old", 48*time.Hour), plant("ghost-young", time.Minute)
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	ctx := context.Background()
	var out bytes.Buffer
	if err := bootstrap.SweepBinary(ctx, s, fhirbinary.SweepOptions{DryRun: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry-run") || !strings.Contains(out.String(), "binary/ghost-old/v1") || strings.Contains(out.String(), "ghost-young") || !exists(old) {
		t.Fatalf("dry-run: %q", out.String())
	}
	out.Reset()
	if err := bootstrap.SweepBinary(ctx, s, fhirbinary.SweepOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if exists(old) || !exists(young) || !strings.Contains(out.String(), "1 mồ côi") || !strings.Contains(out.String(), "đã xóa 1") {
		t.Fatalf("sweep: %q old=%v young=%v", out.String(), exists(old), exists(young))
	}
	// nội dung còn dùng nguyên vẹn
	if g := want(t, call(t, srv, "GET", "/fhir/R4/Binary/"+id, writeSecret, nil), 200); string(g.body) != "còn dùng" {
		t.Fatalf("GET: %q", g.body)
	}

	// kho tắt, hoặc schema chưa migrate: lỗi rõ ràng
	if err := bootstrap.SweepBinary(ctx, settings(), fhirbinary.SweepOptions{}, &out); err == nil || !strings.Contains(err.Error(), "FHIR_BINARY_STORAGE") {
		t.Errorf("kho tắt: %v", err)
	}
	fresh := s
	fresh.DatabaseURL = "sqlite://" + t.TempDir() + "/new.db"
	if err := bootstrap.SweepBinary(ctx, fresh, fhirbinary.SweepOptions{}, &out); err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Errorf("chưa migrate: %v", err)
	}
	// ân hạn quá ngắn bị từ chối
	if err := bootstrap.SweepBinary(ctx, s, fhirbinary.SweepOptions{Grace: time.Minute}, &out); err == nil {
		t.Error("ân hạn 1 phút phải lỗi")
	}
}
