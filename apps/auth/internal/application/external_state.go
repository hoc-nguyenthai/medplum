package application

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"trivita-fhir/apps/auth/internal/domain"
)

// maxStateBytes là trần kích thước `state` nhận vào (Medplum không giới hạn; lệch có chủ đích).
const maxStateBytes = 8 << 10

type signedState struct {
	S domain.ExternalState `json:"s"`
	// N là giá trị của cookie liên kết trình duyệt: callback chỉ nhận khi cookie của chính trình duyệt đó mang cùng giá trị (chống login fixation/CSRF).
	N string `json:"n"`
	E int64  `json:"e"`
}

var b64 = base64.RawURLEncoding

func (s *Service) stateMAC(payload []byte) []byte {
	m := hmac.New(sha256.New, s.External.StateKey)
	m.Write([]byte("auth.external.state.v1|"))
	m.Write(payload)
	return m.Sum(nil)
}

// IssueExternalState ký state và sinh giá trị cookie liên kết. Trả (state, cookieNonce). Đăng nhập ngoài chưa bật thì lỗi.
func (s *Service) IssueExternalState(st domain.ExternalState) (state, cookieNonce string, err error) {
	if !s.External.Enabled() {
		return "", "", domain.Internal("đăng nhập ngoài chưa được bật (thiếu khóa ký state)")
	}
	nb := make([]byte, 16)
	if _, err := rand.Read(nb); err != nil {
		return "", "", domain.InternalErr("sinh nonce state", err)
	}
	cookieNonce = hex.EncodeToString(nb)
	payload, err := json.Marshal(signedState{S: st, N: cookieNonce, E: s.Clock.Now().Add(s.External.ttl()).Unix()})
	if err != nil {
		return "", "", domain.InternalErr("mã hóa state", err)
	}
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(s.stateMAC(payload)), cookieNonce, nil
}

// openExternalState kiểm chữ ký, hạn và cookie liên kết. Mọi lỗi là cùng một "Invalid state" (không lộ lý do).
func (s *Service) openExternalState(token, cookieNonce string) (domain.ExternalState, error) {
	invalid := domain.BadRequest("Invalid state")
	if !s.External.Enabled() || len(token) > maxStateBytes || cookieNonce == "" {
		return domain.ExternalState{}, invalid
	}
	p, m, ok := strings.Cut(token, ".")
	if !ok {
		return domain.ExternalState{}, invalid
	}
	payload, err1 := b64.DecodeString(p)
	mac, err2 := b64.DecodeString(m)
	if err1 != nil || err2 != nil || !hmac.Equal(mac, s.stateMAC(payload)) {
		return domain.ExternalState{}, invalid
	}
	var ss signedState
	if err := json.Unmarshal(payload, &ss); err != nil || s.Clock.Now().Unix() >= ss.E || !hmac.Equal([]byte(ss.N), []byte(cookieNonce)) {
		return domain.ExternalState{}, invalid
	}
	return ss.S, nil
}
