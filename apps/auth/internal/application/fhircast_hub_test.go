package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type fakeTopics struct {
	topic string
	err   error
	asked []string
}

func (f *fakeTopics) TopicFor(_ context.Context, subject string) (string, error) {
	f.asked = append(f.asked, subject)
	return f.topic, f.err
}

func hubService(h *HubConfig) *Service {
	return &Service{Deps{FHIRcast: h, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
}

func TestHasHubScope(t *testing.T) {
	for scope, want := range map[string]bool{
		"fhircast/*": true, "openid fhircast/patient-open": true, "fhircast/": true, "  fhircast/*  ": true,
		"": false, "openid profile": false, "fhircast": false, "xfhircast/*": false, "system/*.cruds": false, "FHIRCAST/*": false,
		"fhir.write fhircastish/x": false,
	} {
		if got := hasHubScope(scope); got != want {
			t.Errorf("hasHubScope(%q) = %v, muốn %v", scope, got, want)
		}
	}
}

func TestAddHubAddsURLAndTopicOnlyForFhircastScope(t *testing.T) {
	ft := &fakeTopics{topic: "topic-1"}
	s := hubService(&HubConfig{URL: "https://ws.example.org/fhircast/STU3", Topics: ft})

	resp := TokenResponse{}
	if oerr := s.addHub(context.Background(), resp, "user-1", "openid fhircast/*"); oerr != nil {
		t.Fatal(oerr)
	}
	if resp["hub.url"] != "https://ws.example.org/fhircast/STU3" || resp["hub.topic"] != "topic-1" {
		t.Errorf("resp: %v", resp)
	}
	if len(ft.asked) != 1 || ft.asked[0] != "user-1" {
		t.Errorf("topic phải được hỏi theo chủ thể của token: %v", ft.asked)
	}

	// scope không có fhircast: không thêm và KHÔNG hỏi kho (không tốn Redis cho token thường)
	ft.asked = nil
	resp = TokenResponse{}
	if oerr := s.addHub(context.Background(), resp, "user-1", "openid profile"); oerr != nil || len(resp) != 0 || len(ft.asked) != 0 {
		t.Errorf("scope thường: %v %v %v", oerr, resp, ft.asked)
	}
}

func TestAddHubDisabledConfigurations(t *testing.T) {
	for name, h := range map[string]*HubConfig{
		"nil":          nil,
		"không có kho": {URL: "https://x", Topics: nil},
		"không có URL": {URL: "", Topics: &fakeTopics{topic: "t"}},
	} {
		resp := TokenResponse{}
		if oerr := hubService(h).addHub(context.Background(), resp, "u", "fhircast/*"); oerr != nil || len(resp) != 0 {
			t.Errorf("%s: %v %v", name, oerr, resp)
		}
	}
}

func TestAddHubFailsClosedWhenTopicUnavailable(t *testing.T) {
	for name, ft := range map[string]*fakeTopics{
		"kho lỗi":    {err: errors.New("redis down")},
		"topic rỗng": {topic: ""},
	} {
		resp := TokenResponse{}
		oerr := hubService(&HubConfig{URL: "https://x", Topics: ft}).addHub(context.Background(), resp, "u", "fhircast/*")
		if oerr == nil || oerr.Code != "server_error" {
			t.Errorf("%s: phải server_error, được %v", name, oerr)
		}
		if _, has := resp["hub.url"]; has || resp["hub.topic"] != nil {
			t.Errorf("%s: không được để lại hub.* một nửa: %v", name, resp)
		}
	}
}
