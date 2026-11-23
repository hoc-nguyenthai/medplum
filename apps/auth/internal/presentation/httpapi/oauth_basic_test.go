package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"
)

func basicReq(raw string) *http.Request {
	r, _ := http.NewRequest("POST", "/oauth2/token", nil)
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(raw)))
	return r
}

func TestClientCredentialsParsingFollowsRFC6749(t *testing.T) {
	cases := []struct {
		name, raw, id, secret string
	}{
		{"đơn giản", "id:secret", "id", "secret"},
		{"secret chứa dấu hai chấm không bị cắt", "id:se:cr:et", "id", "se:cr:et"},
		{"UTF-8", "khách:mật-khẩu", "khách", "mật-khẩu"},
		{"URL-encode", "a%3Ab:c%20d%2B", "a:b", "c d+"},
		{"secret rỗng", "id:", "id", ""},
	}
	for _, c := range cases {
		id, secret, err := clientCredentials(basicReq(c.raw), url.Values{})
		if err != nil || id != c.id || secret != c.secret {
			t.Errorf("%s: %q %q %v", c.name, id, secret, err)
		}
	}
	for _, bad := range []string{"khong-co-dau-hai-cham", string([]byte{0xff, 0xfe, ':', 'x'})} {
		if _, _, err := clientCredentials(basicReq(bad), url.Values{}); err == nil {
			t.Errorf("%q phải bị từ chối", bad)
		}
	}
	// không có header: lấy từ body
	id, secret, err := clientCredentials(&http.Request{Header: http.Header{}}, url.Values{"client_id": {"x"}, "client_secret": {"y"}})
	if err != nil || id != "x" || secret != "y" {
		t.Errorf("body: %q %q %v", id, secret, err)
	}
	// Basic nhưng sai scheme
	r, _ := http.NewRequest("POST", "/", nil)
	r.Header.Set("Authorization", "Bearer abc")
	if _, _, err := clientCredentials(r, url.Values{}); err == nil {
		t.Error("Bearer không phải Basic")
	}
}
