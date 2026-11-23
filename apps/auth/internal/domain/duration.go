package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var tokenDuration = regexp.MustCompile(`^(\d+)([smhdwy])$`)

// ParseTokenDuration đọc thời hạn token dạng `\d+[smhdwy]` (keys.ts:292): 1h, 15m, 2w... Một năm là 365 ngày.
func ParseTokenDuration(s string) (time.Duration, error) {
	m := tokenDuration.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("thời hạn %q không hợp lệ (dạng 15m, 1h, 2w)", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 32)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("thời hạn %q không hợp lệ", s)
	}
	unit := map[string]time.Duration{
		"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour, "y": 365 * 24 * time.Hour,
	}[m[2]]
	return time.Duration(n) * unit, nil
}
