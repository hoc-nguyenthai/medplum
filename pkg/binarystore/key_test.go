package binarystore_test

import (
	"errors"
	"strings"
	"testing"

	"trivita-fhir/pkg/binarystore"
)

func TestKey(t *testing.T) {
	k, err := binarystore.Key("abc-1.2", "v-9")
	if err != nil || k != "binary/abc-1.2/v-9" {
		t.Fatalf("%q, %v", k, err)
	}
	id, ver, err := binarystore.SplitKey(k)
	if err != nil || id != "abc-1.2" || ver != "v-9" {
		t.Fatalf("%q %q %v", id, ver, err)
	}
	long := strings.Repeat("a", 64)
	if _, err := binarystore.Key(long, long); err != nil {
		t.Fatalf("64 ký tự: %v", err)
	}
	for _, c := range [][2]string{
		{"", "v"}, {"a", ""}, {strings.Repeat("a", 65), "v"}, {"a", strings.Repeat("a", 65)}, {"..", "v"}, {"a", ".."}, {".", "v"}, {"a/b", "v"},
		{"a", "v/w"}, {"a\\b", "v"}, {"a b", "v"}, {"a%2e", "v"}, {"a\x00", "v"}, {"é", "v"},
	} {
		if _, err := binarystore.Key(c[0], c[1]); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("Key(%q,%q) = %v", c[0], c[1], err)
		}
	}
	// "..." là id hợp lệ (không phải thành phần đặc biệt của đường dẫn)
	if _, err := binarystore.Key("...", "v"); err != nil {
		t.Errorf("'...': %v", err)
	}
	for _, bad := range []string{"", "binary/a", "binary/a/", "x/a/b", "binary/a/b/c", "binary/../b"} {
		if _, _, err := binarystore.SplitKey(bad); !errors.Is(err, binarystore.ErrInvalidKey) {
			t.Errorf("SplitKey(%q) = %v", bad, err)
		}
	}
}

func TestCheckMetadata(t *testing.T) {
	ext := []string{".ade", ".adp", ".apk", ".appx", ".appxbundle", ".bat", ".cab", ".chm", ".cmd", ".com", ".cpl", ".dll", ".dmg", ".ex", ".ex_", ".exe",
		".hta", ".ins", ".isp", ".iso", ".jar", ".jse", ".lib", ".lnk", ".mde", ".msc", ".msi", ".msix", ".msixbundle", ".msp", ".mst", ".nsh",
		".php", ".pif", ".ps1", ".scr", ".sct", ".shb", ".sys", ".vb", ".vbe", ".vbs", ".vxd", ".wsc", ".wsf", ".wsh"}
	for _, e := range ext {
		if err := binarystore.CheckMetadata("file"+e, ""); !errors.Is(err, binarystore.ErrBlockedFileExtension) {
			t.Errorf("%s: %v", e, err)
		}
	}
	for _, name := range []string{"A.EXE", "a.Exe", "a.exe.", "a.exe ", "a.exe...", "a.exe \t.", "x.tar.gz.exe"} {
		if err := binarystore.CheckMetadata(name, ""); !errors.Is(err, binarystore.ErrBlockedFileExtension) {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"", "report.pdf", "exe", "a.exe.txt", "image.png", "notes.txt", "a.executable", "a.combo"} {
		if err := binarystore.CheckMetadata(name, ""); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, ct := range []string{"application/java-archive", "application/x-msdownload", "application/x-sh", "application/vnd.apple.installer+xml",
		"application/vnd.microsoft.portable-executable", "APPLICATION/X-SH", "application/x-sh;charset=utf-8", "application/x-sh ; charset=utf-8", "  application/x-sh"} {
		if err := binarystore.CheckMetadata("", ct); !errors.Is(err, binarystore.ErrBlockedContentType) {
			t.Errorf("%q: %v", ct, err)
		}
	}
	for _, ct := range []string{"", "text/plain", "application/pdf", "application/dicom", "application/x-shellscript", "application/octet-stream"} {
		if err := binarystore.CheckMetadata("", ct); err != nil {
			t.Errorf("%q: %v", ct, err)
		}
	}
	if err := binarystore.CheckMetadata("a.exe", "text/plain"); !errors.Is(err, binarystore.ErrBlockedFileExtension) {
		t.Errorf("đuôi chặn đi trước: %v", err)
	}
	if err := binarystore.CheckMetadata("a.txt", "application/x-sh"); !errors.Is(err, binarystore.ErrBlockedContentType) {
		t.Errorf("loại chặn: %v", err)
	}
}
