package binarystore

import "strings"

// Đuôi tệp bị chặn (so trên tên viết thường; Medplum storage/utils.ts).
var blockedExtensions = []string{
	".ade", ".adp", ".apk", ".appx", ".appxbundle", ".bat", ".cab", ".chm", ".cmd", ".com", ".cpl", ".dll", ".dmg", ".ex", ".ex_", ".exe",
	".hta", ".ins", ".isp", ".iso", ".jar", ".jse", ".lib", ".lnk", ".mde", ".msc", ".msi", ".msix", ".msixbundle", ".msp", ".mst", ".nsh",
	".php", ".pif", ".ps1", ".scr", ".sct", ".shb", ".sys", ".vb", ".vbe", ".vbs", ".vxd", ".wsc", ".wsf", ".wsh",
}

var blockedContentTypes = map[string]bool{
	"application/java-archive":                      true,
	"application/x-msdownload":                      true,
	"application/x-sh":                              true,
	"application/vnd.apple.installer+xml":           true,
	"application/vnd.microsoft.portable-executable": true,
}

// CheckMetadata từ chối tên tệp có đuôi nguy hiểm (ErrBlockedFileExtension) hoặc loại nội dung nguy hiểm (ErrBlockedContentType).
// Khác Medplum: bỏ tham số của loại nội dung ("application/x-sh; charset=utf-8" vẫn bị chặn) và bỏ dấu cách/dấu chấm cuối tên
// ("a.exe." hay "a.exe " vẫn bị chặn, vì Windows bỏ chúng khi mở). Chuỗi rỗng thì không kiểm phần đó.
func CheckMetadata(filename, contentType string) error {
	if filename != "" {
		name := strings.ToLower(strings.TrimRight(filename, " .\t"))
		for _, ext := range blockedExtensions {
			if strings.HasSuffix(name, ext) {
				return ErrBlockedFileExtension
			}
		}
	}
	if contentType != "" {
		base, _, _ := strings.Cut(contentType, ";")
		if blockedContentTypes[strings.ToLower(strings.TrimSpace(base))] {
			return ErrBlockedContentType
		}
	}
	return nil
}
