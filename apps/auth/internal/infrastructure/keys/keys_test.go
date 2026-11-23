package keys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func claims() map[string]any {
	return map[string]any{"iss": "i", "sub": "u", "exp": time.Now().Add(time.Hour).Unix()}
}

func TestGenerateLoadSignVerifyEachAlgorithm(t *testing.T) {
	for _, alg := range []Alg{ES256, ES384, RS256} {
		dir := t.TempDir()
		kid, err := Generate(dir, alg)
		if err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Stat(filepath.Join(dir, kid+".pem"))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: quyền file %v, muốn 0600", alg, fi.Mode().Perm())
		}
		st, err := Load(dir, "")
		if err != nil || st.SigningKID() != kid || st.SigningAlg() != string(alg) {
			t.Fatalf("%s: %v %v", alg, st, err)
		}
		raw, err := st.Sign(claims())
		if err != nil {
			t.Fatal(err)
		}
		tok, _, _ := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
		if tok.Header["kid"] != kid || tok.Header["alg"] != string(alg) || tok.Header["typ"] != "JWT" {
			t.Errorf("%s: header %v", alg, tok.Header)
		}
		got, err := st.Verify(raw)
		if err != nil || got["sub"] != "u" {
			t.Errorf("%s: %v %v", alg, got, err)
		}
	}
}

func TestJWKSHasOnlyPublicParameters(t *testing.T) {
	for _, alg := range []Alg{RS256, ES256} {
		st, err := Ephemeral(alg)
		if err != nil {
			t.Fatal(err)
		}
		body := st.JWKS()
		var set struct {
			Keys []map[string]any `json:"keys"`
		}
		if err := json.Unmarshal(body, &set); err != nil || len(set.Keys) != 1 {
			t.Fatalf("%s: %s %v", alg, body, err)
		}
		k := set.Keys[0]
		if k["kid"] != st.SigningKID() || k["alg"] != string(alg) || k["use"] != "sig" {
			t.Errorf("%s: %v", alg, k)
		}
		for _, private := range []string{"d", "p", "q", "dp", "dq", "qi", "k"} {
			if _, has := k[private]; has {
				t.Errorf("%s: JWKS lộ tham số riêng %q", alg, private)
			}
		}
	}
}

func TestVerifyRejectsForgeriesAndWrongKeys(t *testing.T) {
	st, _ := Ephemeral(ES256)
	other, _ := Ephemeral(ES256)
	good, _ := st.Sign(claims())

	if _, err := other.Verify(good); err == nil {
		t.Error("khóa khác không được xác minh")
	}
	expired := claims()
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	if raw, _ := st.Sign(expired); true {
		if _, err := st.Verify(raw); err == nil {
			t.Error("token hết hạn phải bị từ chối")
		}
	}
	noExp := map[string]any{"iss": "i"}
	if raw, _ := st.Sign(noExp); true {
		if _, err := st.Verify(raw); err == nil {
			t.Error("token không có exp phải bị từ chối")
		}
	}
	// alg=none
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims(claims()))
	none.Header["kid"] = st.SigningKID()
	raw, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := st.Verify(raw); err == nil {
		t.Error("alg=none phải bị từ chối")
	}
	// HS256 ký bằng chính JWKS công khai làm secret (tấn công nhầm thuật toán)
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims()))
	hs.Header["kid"] = st.SigningKID()
	raw, _ = hs.SignedString(st.JWKS())
	if _, err := st.Verify(raw); err == nil {
		t.Error("HS256 phải bị từ chối")
	}
	// thiếu kid, kid lạ
	for _, kid := range []any{nil, "lạ"} {
		tk := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims(claims()))
		if kid != nil {
			tk.Header["kid"] = kid
		}
		raw, _ := tk.SignedString(mustEC(t, st))
		if _, err := st.Verify(raw); err == nil {
			t.Errorf("kid=%v phải bị từ chối", kid)
		}
	}
	// chỉnh sửa payload
	parts := strings.Split(good, ".")
	if _, err := st.Verify(parts[0] + "." + parts[1] + "x." + parts[2]); err == nil {
		t.Error("payload bị sửa phải bị từ chối")
	}
	if _, err := st.Verify("không phải jwt"); err == nil {
		t.Error("rác phải bị từ chối")
	}
}

func mustEC(t *testing.T, st *Store) any {
	t.Helper()
	return st.signing.priv // cùng khóa thật nhưng thiếu/lạ kid
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	oldKID, _ := Generate(dir, ES256)
	oldStore, _ := Load(dir, "")
	oldToken, _ := oldStore.Sign(claims())

	newKID, _ := Generate(dir, ES256)
	// nhiều khóa mà không chỉ rõ khóa ký: lỗi, không đoán
	if _, err := Load(dir, ""); err == nil || !strings.Contains(err.Error(), "AUTH_ACTIVE_KID") {
		t.Fatalf("muốn lỗi yêu cầu khóa ký: %v", err)
	}
	if _, err := Load(dir, "không-có"); err == nil {
		t.Error("kid không tồn tại phải lỗi")
	}
	st, err := Load(dir, newKID)
	if err != nil || st.SigningKID() != newKID || st.KeyCount() != 2 {
		t.Fatalf("%v %v", st, err)
	}
	if _, err := st.Verify(oldToken); err != nil {
		t.Errorf("token ký bằng khóa cũ vẫn phải xác minh được sau khi đổi khóa ký: %v", err)
	}
	fresh, _ := st.Sign(claims())
	if tok, _, _ := jwt.NewParser().ParseUnverified(fresh, jwt.MapClaims{}); tok.Header["kid"] != newKID {
		t.Error("token mới phải ký bằng khóa mới")
	}
	if !strings.Contains(string(st.JWKS()), oldKID) || !strings.Contains(string(st.JWKS()), newKID) {
		t.Error("JWKS phải chứa cả hai khóa")
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(t.TempDir(), ""); err == nil {
		t.Error("thư mục rỗng phải lỗi")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "x.pem"), []byte("rác"), 0o600)
	if _, err := Load(dir, ""); err == nil {
		t.Error("PEM hỏng phải lỗi")
	}
	if _, err := Generate(t.TempDir(), "HS256"); err == nil {
		t.Error("thuật toán lạ phải lỗi")
	}
	if _, err := ParseAlg("hs256"); err == nil {
		t.Error("ParseAlg")
	}
	if a, err := ParseAlg("es384"); err != nil || a != ES384 {
		t.Error("ParseAlg es384")
	}
	if a, _ := ParseAlg(""); a != RS256 {
		t.Error("mặc định RS256")
	}
}
