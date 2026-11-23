package envconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/envconfig"
)

func TestReaderParsesAndCollectsErrors(t *testing.T) {
	env := map[string]string{"S": "x", "B": "true", "I": "7", "D1": "1.5", "D2": "250ms",
		"BADB": "maybe", "BADI": "x", "BADD": "soon"}
	r := envconfig.NewReader(func(k string) string { return env[k] })
	s, def, b, i := "", "keep", false, 0
	var d1, d2, d3 time.Duration = 0, 0, time.Second
	r.String(&s, "S")
	r.String(&def, "NOPE")
	r.Bool(&b, "B")
	r.Int(&i, "I")
	r.Duration(&d1, "D1")
	r.Duration(&d2, "D2")
	r.Duration(&d3, "NOPE")
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if s != "x" || def != "keep" || !b || i != 7 || d1 != 1500*time.Millisecond || d2 != 250*time.Millisecond || d3 != time.Second {
		t.Fatalf("giá trị sai: %v %v %v %v %v %v %v", s, def, b, i, d1, d2, d3)
	}
	r.Bool(&b, "BADB")
	r.Int(&i, "BADI")
	r.Duration(&d1, "BADD")
	r.Add(envconfig.OneOf("X", "z", "a", "b"))
	for _, want := range []string{"BADB", "BADI", "BADD", "X"} {
		if r.Err() == nil || !strings.Contains(r.Err().Error(), want) {
			t.Errorf("thiếu lỗi %s: %v", want, r.Err())
		}
	}
	if envconfig.OneOf("X", "a", "a", "b") != nil {
		t.Fatal("giá trị hợp lệ")
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := envconfig.LoadDotEnv(path); err != nil {
		t.Fatalf("thiếu file không phải lỗi: %v", err)
	}
	_ = os.WriteFile(path, []byte(strings.Join([]string{
		"# c", "", "export EC_A=1", `EC_B="two words"`, "EC_C='x'", "EC_D=v # ghi chú", "EC_E=real",
	}, "\n")), 0o600)
	t.Setenv("EC_E", "from-env")
	for _, k := range []string{"EC_A", "EC_B", "EC_C", "EC_D"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	if err := envconfig.LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("EC_A") != "1" || os.Getenv("EC_B") != "two words" || os.Getenv("EC_C") != "x" ||
		os.Getenv("EC_D") != "v" || os.Getenv("EC_E") != "from-env" {
		t.Fatal("nạp .env sai")
	}
	_ = os.WriteFile(path, []byte("KHONG_CO_DAU\n"), 0o600)
	if err := envconfig.LoadDotEnv(path); err == nil || !strings.Contains(err.Error(), ":1") {
		t.Fatalf("dòng hỏng: %v", err)
	}
	if err := envconfig.LoadDotEnv(dir); err == nil {
		t.Fatal("thư mục không đọc được phải lỗi")
	}
}
