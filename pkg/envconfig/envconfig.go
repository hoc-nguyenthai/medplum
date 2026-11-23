// Package envconfig đọc cấu hình từ biến môi trường: gom mọi lỗi một lần và nạp file .env.
package envconfig

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Reader đọc từng biến vào con trỏ đích (giữ nguyên giá trị mặc định nếu biến trống) và gom lỗi.
type Reader struct {
	get  func(string) string
	errs []error
}

// NewReader nhận hàm tra cứu (os.Getenv, hoặc map trong test).
func NewReader(get func(string) string) *Reader { return &Reader{get: get} }

func (r *Reader) String(dst *string, key string) {
	if v := r.get(key); v != "" {
		*dst = v
	}
}

func (r *Reader) Bool(dst *bool, key string) {
	if v := r.get(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			r.errs = append(r.errs, fmt.Errorf("%s: %q không phải boolean", key, v))
			return
		}
		*dst = b
	}
}

func (r *Reader) Int(dst *int, key string) {
	if v := r.get(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			r.errs = append(r.errs, fmt.Errorf("%s: %q không phải số nguyên", key, v))
			return
		}
		*dst = n
	}
}

// Float đọc số thực (không có thì giữ mặc định).
func (r *Reader) Float(dst *float64, key string) {
	if v := r.get(key); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			r.errs = append(r.errs, fmt.Errorf("%s: %q không phải số", key, v))
			return
		}
		*dst = f
	}
}

// Duration nhận số giây (số thực, như bản Python) hoặc dạng Go như "1500ms", "25s".
func (r *Reader) Duration(dst *time.Duration, key string) {
	v := r.get(key)
	if v == "" {
		return
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		*dst = time.Duration(f * float64(time.Second))
		return
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: %q không phải thời gian", key, v))
		return
	}
	*dst = d
}

// Add ghi thêm lỗi kiểm tra hợp lệ do app tự phát hiện.
func (r *Reader) Add(errs ...error) { r.errs = append(r.errs, errs...) }

// Err trả về mọi lỗi đã gom (nil nếu không có).
func (r *Reader) Err() error { return errors.Join(r.errs...) }

// OneOf kiểm tra value thuộc tập cho phép.
func OneOf(name, value string, allowed ...string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s: %q không hợp lệ, chọn một trong %s", name, value, strings.Join(allowed, "|"))
}

// LoadDotEnv nạp file KEY=VALUE vào môi trường mà không ghi đè biến đã có. File không tồn tại thì bỏ qua.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: dòng không có dấu '='", path, n)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		} else if i := strings.Index(val, " #"); i >= 0 { // bỏ comment cuối dòng
			val = strings.TrimSpace(val[:i])
		}
		if _, set := os.LookupEnv(key); !set {
			if err := os.Setenv(key, val); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
