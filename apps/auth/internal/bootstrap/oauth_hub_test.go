package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"testing"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"

	"github.com/alicebob/miniredis/v2"
)

const hubURL = "http://gateway.test:8007/fhircast/STU3"

func withHub(s *config.Settings) { s.FHIRcastHubURL = hubURL + "/" } // dấu "/" cuối phải bị bỏ khi trả cho client

// Phản hồi token có hub.url và hub.topic khi scope có fhircast/, topic ổn định giữa hai lần xin, và access token mang đúng scope
// và project để ws-gateway (cùng bộ xác minh fhirauth, JWKS của auth) chấp nhận.
func TestTokenCarriesHubURLAndStableTopic(t *testing.T) {
	a := newAPI(t, withHub, nil)
	r1 := a.clientToken(url.Values{"scope": {"fhircast/*"}})
	r2 := a.clientToken(url.Values{"scope": {"fhircast/*"}})
	if r1.status != 200 || r2.status != 200 {
		t.Fatalf("%d %s | %d %s", r1.status, r1.body, r2.status, r2.body)
	}
	b1, b2 := r1.json(), r2.json()
	if b1["hub.url"] != hubURL || b2["hub.url"] != hubURL {
		t.Errorf("hub.url = %v, muốn %s", b1["hub.url"], hubURL)
	}
	topic, _ := b1["hub.topic"].(string)
	if topic == "" || b2["hub.topic"] != topic {
		t.Errorf("topic phải ổn định giữa hai lần xin: %v vs %v", topic, b2["hub.topic"])
	}
	claims, err := a.verifier().VerifyClaims(context.Background(), b1["access_token"].(string))
	if err != nil {
		t.Fatalf("bộ xác minh của gateway từ chối token: %v", err)
	}
	if claims.Project != a.seed.Project.ID || !slices.Contains(claims.Scopes, "fhircast/*") {
		t.Errorf("claim project/scope không đủ cho gateway: %+v", claims)
	}
}

func TestTokenWithoutFhircastScopeHasNoHubFields(t *testing.T) {
	a := newAPI(t, withHub, nil)
	body := a.clientToken(nil).json()
	if _, has := body["hub.url"]; has {
		t.Errorf("token thường có hub.url: %v", body)
	}
	if _, has := body["hub.topic"]; has {
		t.Errorf("token thường có hub.topic: %v", body)
	}
}

func TestHubDisabledByDefaultEvenWithFhircastScope(t *testing.T) {
	a := newAPI(t, nil, nil)
	r := a.clientToken(url.Values{"scope": {"fhircast/*"}})
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	body := r.json()
	if _, has := body["hub.url"]; has {
		t.Errorf("chưa cấu hình hub mà vẫn trả hub.url: %v", body)
	}
	if _, has := body["hub.topic"]; has {
		t.Errorf("chưa cấu hình hub mà vẫn trả hub.topic: %v", body)
	}
}

type failingTopics struct{}

func (failingTopics) TopicFor(context.Context, string) (string, error) {
	return "", errors.New("redis down")
}

// Không lấy được topic thì KHÔNG cấp token (fail đóng, như Medplum); token thường không bị ảnh hưởng.
func TestTokenFailsClosedWhenTopicStoreIsDown(t *testing.T) {
	a := newAPI(t, withHub, nil)
	a.c.Auth.FHIRcast = &application.HubConfig{URL: hubURL, Topics: failingTopics{}}
	r := a.clientToken(url.Values{"scope": {"fhircast/*"}})
	if r.status != 500 || r.json()["error"] != "server_error" || r.json()["access_token"] != nil {
		t.Fatalf("phải 500 server_error và không có token: %d %s", r.status, r.body)
	}
	if ok := a.clientToken(nil); ok.status != 200 {
		t.Errorf("token thường phải vẫn cấp được khi kho topic hỏng: %d %s", ok.status, ok.body)
	}
}

// Có AUTH_REDIS_URL thì topic nằm ở Redis dùng chung (nhiều instance cùng thấy), không phải bộ nhớ của từng instance.
func TestHubTopicsLiveInSharedRedisWhenConfigured(t *testing.T) {
	mr := miniredis.RunT(t)
	a := newAPI(t, func(s *config.Settings) { withHub(s); s.RedisURL = "redis://" + mr.Addr() }, nil)
	body := a.clientToken(url.Values{"scope": {"fhircast/*"}}).json()
	topic, _ := body["hub.topic"].(string)
	if topic == "" {
		t.Fatalf("thiếu hub.topic: %v", body)
	}
	found := false
	for _, k := range mr.Keys() {
		if v, _ := mr.Get(k); v == topic {
			found = true
			if mr.TTL(k) <= 0 {
				t.Errorf("khóa %s không có hạn", k)
			}
		}
	}
	if !found {
		t.Errorf("topic %q không nằm ở Redis (khóa: %v)", topic, mr.Keys())
	}
}
