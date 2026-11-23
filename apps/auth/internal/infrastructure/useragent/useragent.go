// Package useragent tách tên trình duyệt và hệ điều hành từ chuỗi User-Agent, đủ cho danh sách phiên của /auth/me. Cố ý tối
// giản (không phụ thuộc thư viện ngoài); chuỗi không nhận ra trả rỗng.
package useragent

import "strings"

// Parser cài application.UserAgentParser.
type Parser struct{}

// Parse trả (trình duyệt, hệ điều hành). Thứ tự kiểm tra quan trọng vì nhiều UA chứa tên của nhau (Edge có "Chrome",
// Chrome có "Safari", iOS có "Mac OS X").
func (Parser) Parse(ua string) (browser, os string) {
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(ua, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("Edg/", "EdgA/", "EdgiOS/"):
		browser = "Microsoft Edge"
	case has("OPR/", "Opera"):
		browser = "Opera"
	case has("Firefox/", "FxiOS/"):
		browser = "Firefox"
	case has("Chrome/", "CriOS/"):
		browser = "Chrome"
	case has("Safari/") && has("Version/"):
		browser = "Safari"
	case has("curl/"):
		browser = "curl"
	}
	switch {
	case has("Windows"):
		os = "Windows"
	case has("iPhone", "iPad", "iPod"):
		os = "iOS"
	case has("Android"):
		os = "Android"
	case has("Mac OS X", "Macintosh"):
		os = "macOS"
	case has("CrOS"):
		os = "Chrome OS"
	case has("Linux", "X11"):
		os = "Linux"
	}
	return browser, os
}
