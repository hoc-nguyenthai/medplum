package fhirsub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"trivita-fhir/pkg/core"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirrouter"
)

// Token bind của websocket. Tách khỏi Hub để hai tiến trình dùng chung một định dạng:
//   - bên CẤP (fhir-server, có Repository và Principal của người gọi): Tokens.Issue / Tokens.TokenOperation;
//   - bên NHẬN (Hub, ở fhir-server nhúng hoặc ở ws-gateway): Tokens.verify / Tokens.parse.
//
// Token là `base64url(claims).base64url(HMAC-SHA256("fhir-ws-binding|" + claims))`. Hai bên chia sẻ khóa HMAC; không bên nào
// cần cơ sở dữ liệu hay Principal sống vì quyền của người xin được chụp vào claims (Snap).
//
// Xoay khóa: ký luôn bằng khóa hiện hành (Signing); xác minh thử khóa hiện hành rồi các khóa trước (Previous). Quy trình:
// (1) đặt khóa mới vào MỌI bên nhận với khóa cũ ở Previous; (2) đổi bên cấp sang khóa mới; (3) sau một TokenTTL bỏ Previous.
// Khóa quá hạn bị từ chối mà không phải ngắt kết nối nào: token chỉ dùng lúc bind, kết nối đã bind không phụ thuộc khóa.

// TokenVersion là phiên bản định dạng claims. Bên nhận từ chối phiên bản lạ (lớn hơn) để deploy cuốn chiếu không âm thầm đọc sai
// token của bản mới; token không có trường `v` (bản trước WS-3) được coi là phiên bản 1 và hết hạn trong một TokenTTL.
const TokenVersion = 1

// MaxPreviousKeys là số khóa trước tối đa được chấp nhận khi xác minh.
const MaxPreviousKeys = 3

const tokenDomain = "fhir-ws-binding|"

// TokenConfig cấu hình Tokens; giá trị 0 dùng mặc định trong ngoặc.
type TokenConfig struct {
	SigningKey []byte   // khóa ký và xác minh hiện hành (>= 32 byte)
	Previous   [][]byte // khóa cũ chỉ để xác minh trong lúc xoay khóa (mỗi khóa >= 32 byte, tối đa MaxPreviousKeys)
	URL        string   // websocket-url trả về cùng token ("/ws/subscriptions-r4" nếu rỗng)
	TTL        time.Duration
	Now        func() time.Time
}

// Tokens cấp và xác minh token bind. An toàn cho nhiều goroutine (không có trạng thái thay đổi).
type Tokens struct {
	signing  []byte
	previous [][]byte
	url      string
	ttl      time.Duration
	now      func() time.Time
}

const minKeyLen = 32

// NewTokens kiểm khóa rồi dựng Tokens.
func NewTokens(cfg TokenConfig) (*Tokens, error) {
	if len(cfg.SigningKey) < minKeyLen {
		return nil, errors.New("fhirsub: khóa ký token websocket phải dài ít nhất 32 byte")
	}
	if len(cfg.Previous) > MaxPreviousKeys {
		return nil, errors.New("fhirsub: quá nhiều khóa token websocket cũ")
	}
	for _, k := range cfg.Previous {
		if len(k) < minKeyLen {
			return nil, errors.New("fhirsub: khóa token websocket cũ phải dài ít nhất 32 byte")
		}
	}
	t := &Tokens{signing: cfg.SigningKey, previous: cfg.Previous, url: cfg.URL, ttl: cfg.TTL, now: cfg.Now}
	if t.url == "" {
		t.url = "/ws/subscriptions-r4"
	}
	if t.ttl <= 0 {
		t.ttl = 5 * time.Minute
	}
	if t.now == nil {
		t.now = time.Now
	}
	return t, nil
}

type tokenClaims struct {
	Version int    `json:"v"`
	Sub     string `json:"sub"`  // id Subscription
	Project string `json:"proj"` // project của Subscription
	Exp     int64  `json:"exp"`  // unix giây
	ID      string `json:"jti"`
	// Snap là ảnh chụp quyền của người lấy token (fhirauth.Principal.MarshalSnapshot); rỗng khi không có xác thực.
	Snap json.RawMessage `json:"snap,omitempty"`
}

func sign(key, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(tokenDomain))
	m.Write(payload)
	return m.Sum(nil)
}

// Issue cấp token bind Subscription subID của project với ảnh chụp quyền snap (nil: không phân quyền).
func (t *Tokens) Issue(subID, project string, snap json.RawMessage) (token string, exp time.Time) {
	exp = t.now().Add(t.ttl).UTC()
	raw, _ := json.Marshal(tokenClaims{Version: TokenVersion, Sub: subID, Project: project, Exp: exp.Unix(), ID: core.NewID(), Snap: snap})
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(sign(t.signing, raw)), exp
}

// verify kiểm chữ ký, phiên bản và hạn của token.
func (t *Tokens) verify(token string) (*tokenClaims, error) {
	c, err := t.parse(token)
	if err != nil {
		return nil, err
	}
	if t.now().Unix() >= c.Exp {
		return nil, errors.New("token đã hết hạn")
	}
	return c, nil
}

// parse chỉ kiểm chữ ký, phiên bản và định dạng (không kiểm hạn): dùng cho unbind, nơi token chỉ nêu Subscription cần gỡ khỏi
// chính kết nối của người gửi nên token hết hạn không làm tăng quyền của ai.
func (t *Tokens) parse(token string) (*tokenClaims, error) {
	a, b, ok := strings.Cut(token, ".")
	if !ok {
		return nil, errors.New("token không hợp lệ")
	}
	raw, err1 := base64.RawURLEncoding.DecodeString(a)
	sig, err2 := base64.RawURLEncoding.DecodeString(b)
	if err1 != nil || err2 != nil || !t.validSignature(raw, sig) {
		return nil, errors.New("token không hợp lệ")
	}
	var c tokenClaims
	if json.Unmarshal(raw, &c) != nil || c.Sub == "" {
		return nil, errors.New("token không hợp lệ")
	}
	if c.Version == 0 {
		c.Version = 1 // token của bản trước WS-3
	}
	if c.Version > TokenVersion {
		return nil, errors.New("token có phiên bản không được hỗ trợ")
	}
	return &c, nil
}

// validSignature thử mọi khóa (không thoát sớm theo khóa nào khớp để thời gian không lộ khóa nào được dùng).
func (t *Tokens) validSignature(raw, sig []byte) bool {
	ok := hmac.Equal(sig, sign(t.signing, raw))
	for _, k := range t.previous {
		if hmac.Equal(sig, sign(k, raw)) {
			ok = true
		}
	}
	return ok
}

// TokenOperation là handler của "Subscription/:id/$get-ws-binding-token". repo nên là repository đã tách project và
// quyền (cùng repository của router) để chỉ người đọc được Subscription mới lấy được token.
func (t *Tokens) TokenOperation(repo fhirrouter.Repository) fhirrouter.Handler {
	return func(ctx context.Context, req *fhirrouter.Request) (*fhirrouter.Response, error) {
		res, err := repo.ReadResource(ctx, "Subscription", req.Params["id"])
		if err != nil {
			return nil, err
		}
		sub, ok := res.(*fhir.Subscription)
		if !ok {
			return nil, fhirrouter.NotFound()
		}
		if sub.Channel == nil || sub.Channel.Type != "websocket" {
			return nil, fhirrouter.BadRequest("Subscription không dùng kênh websocket")
		}
		if sub.Status != "active" && sub.Status != "requested" {
			return nil, fhirrouter.BadRequest("Subscription không hoạt động (status=" + sub.Status + ")")
		}
		project := ""
		if sub.Meta != nil {
			project = sub.Meta.Project
		}
		var snap json.RawMessage
		if p, ok := fhirauth.PrincipalFrom(ctx); ok {
			if snap, err = p.MarshalSnapshot(); err != nil {
				return nil, err
			}
		}
		token, exp := t.Issue(sub.ID, project, snap)
		return &fhirrouter.Response{Status: 200, Resource: &fhir.Parameters{Parameter: []fhir.ParametersParameter{
			{Name: "token", ValueString: token},
			{Name: "expiration", ValueDateTime: exp.Format(time.RFC3339)},
			{Name: "websocket-url", ValueUrl: t.url},
		}}}, nil
	}
}
