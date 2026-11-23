package bootstrap

import (
	"fmt"
	"io"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/infrastructure/keys"
)

// KeysUsage là cách dùng lệnh `auth keys`.
const KeysUsage = `  auth keys generate [-alg RS256|ES256|ES384] [-dir <thư mục>]
        sinh khóa ký mới (PEM, quyền 0600) vào thư mục (mặc định AUTH_SIGNING_KEY_PATH) và in kid. Xoay khóa: sinh khóa mới,
        triển khai để JWKS có cả hai, đợi quá TTL cache của người xác minh, rồi đặt AUTH_ACTIVE_KID là kid mới
  auth keys list [-dir <thư mục>]
        liệt kê kid, thuật toán của các khóa trong thư mục và khóa đang ký`

// RunKeys chạy `auth keys ...`; không mở database.
func RunKeys(s config.Settings, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("cách dùng:\n%s", KeysUsage)
	}
	fs := newFlags("keys " + args[0])
	alg := fs.String("alg", s.SigningAlg, "thuật toán")
	dir := fs.String("dir", s.SigningKeyPath, "thư mục khóa")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("%v\n%s", err, KeysUsage)
	}
	if *dir == "" {
		return fmt.Errorf("thiếu thư mục khóa: đặt -dir hoặc AUTH_SIGNING_KEY_PATH")
	}
	switch args[0] {
	case "generate":
		a, err := keys.ParseAlg(*alg)
		if err != nil {
			return err
		}
		kid, err := keys.Generate(*dir, a)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "đã sinh khóa %s (%s) trong %s\nđể dùng làm khóa ký: AUTH_ACTIVE_KID=%s\n", kid, a, *dir, kid)
		return err
	case "list":
		st, err := keys.Load(*dir, s.ActiveKID)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%d khóa; khóa ký: %s (%s)\n", st.KeyCount(), st.SigningKID(), st.SigningAlg())
		return err
	}
	return fmt.Errorf("lệnh keys không hợp lệ: %q\n%s", args[0], KeysUsage)
}
