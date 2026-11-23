package mailer

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	pkgmailer "trivita-fhir/pkg/mailer"
)

var _ application.Mailer = Log{}
var _ application.Mailer = None{}
var _ application.Mailer = (*Fake)(nil)
var _ application.Mailer = Adapter{}

func TestLogWritesTheLink(t *testing.T) {
	var buf bytes.Buffer
	l := Log{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	if err := l.Send(context.Background(), application.Message{To: "a@example.org", Subject: "S", Text: "https://app/x/1/secret"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "https://app/x/1/secret") || !strings.Contains(buf.String(), "a@example.org") {
		t.Errorf("log phải có người nhận và link: %s", buf.String())
	}
}

func TestNoneNeverLogsTheBody(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	if err := (None{}).Send(context.Background(), application.Message{To: "a@example.org", Subject: "S", Text: "https://app/x/1/secret"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "secret") || strings.Contains(buf.String(), "a@example.org") {
		t.Errorf("provider none không được ghi nội dung thư hay người nhận: %s", buf.String())
	}
}

func TestFakeRecordsEvenOnError(t *testing.T) {
	f := &Fake{Err: errors.New("down")}
	if err := f.Send(context.Background(), application.Message{To: "a"}); err == nil {
		t.Fatal("phải trả lỗi đã đặt")
	}
	got := f.Sent()
	if len(got) != 1 || got[0].To != "a" {
		t.Fatalf("%+v", got)
	}
	got[0].To = "đã sửa"
	if f.Sent()[0].To != "a" {
		t.Error("Sent phải trả bản sao")
	}
}

func TestAdapterBridgesToPkgMailerAndHidesServerDetails(t *testing.T) {
	f := &pkgmailer.Fake{}
	a := Adapter{Inner: f}
	if err := a.Send(context.Background(), application.Message{To: "a@x.org", Subject: "S", Text: "T"}); err != nil {
		t.Fatal(err)
	}
	got := f.Sent()
	if len(got) != 1 || len(got[0].To) != 1 || got[0].To[0].Email != "a@x.org" || got[0].Subject != "S" || got[0].Text != "T" {
		t.Fatalf("%+v", got)
	}
	// lỗi có phân loại: chỉ thông điệp an toàn, không lộ nguyên nhân (có thể chứa địa chỉ người nhận)
	f.Err = &pkgmailer.Error{Kind: pkgmailer.KindTransport, Msg: "Failed to send email", Err: errors.New("550 <victim@x.org>: no such user")}
	err := a.Send(context.Background(), application.Message{To: "a@x.org", Subject: "S"})
	if err == nil || strings.Contains(err.Error(), "victim") || !strings.Contains(err.Error(), "Failed to send email") {
		t.Errorf("lỗi phân loại: %v", err)
	}
	f.Err = errors.New("lỗi lạ chứa victim@x.org")
	if err := a.Send(context.Background(), application.Message{To: "a@x.org", Subject: "S"}); err == nil || strings.Contains(err.Error(), "victim") {
		t.Errorf("lỗi lạ: %v", err)
	}
}
