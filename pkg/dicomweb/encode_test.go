package dicomweb_test

import (
	"errors"
	"testing"

	"trivita-fhir/pkg/dicomweb"
)

func TestTransferSyntaxOfBulk(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "1.2.840.10008.1.2.1",
		"application/octet-stream": "1.2.840.10008.1.2.1",
		"application/octet-stream; transfer-syntax=1.2.840.10008.1.2": "1.2.840.10008.1.2.1",
		"image/jpeg": "1.2.840.10008.1.2.4.50",
		"image/jpeg; transfer-syntax=1.2.840.10008.1.2.4.70": "1.2.840.10008.1.2.4.70",
		"image/jls":  "1.2.840.10008.1.2.4.80",
		"image/jp2":  "1.2.840.10008.1.2.4.90",
		"image/jphc": "1.2.840.10008.1.2.4.201",
	} {
		if got, err := dicomweb.TransferSyntaxOfBulk(in); err != nil || got != want {
			t.Errorf("%q: %q %v, muốn %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"text/plain", "image/png", "image/jpeg; transfer-syntax=1.2.840.10008.1.2.2", "image/jpeg; transfer-syntax=1.2.840.10008.1.2.1.99", "image/jpeg; transfer-syntax=abc", ";;;"} {
		if _, err := dicomweb.TransferSyntaxOfBulk(bad); !errors.Is(err, dicomweb.ErrEncode) {
			t.Errorf("%q phải bị từ chối: %v", bad, err)
		}
	}
}

func TestParseMetadataJSON(t *testing.T) {
	one, err := dicomweb.ParseMetadataJSON([]byte(` {"00080018":{"vr":"UI","Value":["1.2"]}} `))
	if err != nil || len(one) != 1 || one[0].Str("00080018") != "1.2" {
		t.Errorf("dataset đơn: %v %v", one, err)
	}
	many, err := dicomweb.ParseMetadataJSON([]byte(`[{},{}]`))
	if err != nil || len(many) != 2 {
		t.Errorf("mảng: %v %v", many, err)
	}
	for _, bad := range []string{``, `[]`, `[1]`, `"x"`, `{"a":1}`, `[{"00080018":`} {
		if _, err := dicomweb.ParseMetadataJSON([]byte(bad)); !errors.Is(err, dicomweb.ErrEncode) {
			t.Errorf("%q phải bị từ chối: %v", bad, err)
		}
	}
}

func TestBuildPart10RequiresUIDs(t *testing.T) {
	_, _, err := dicomweb.BuildPart10(dicomweb.Dataset{"00080018": {VR: "UI", Value: []any{"1.2"}}}, nil)
	if !errors.Is(err, dicomweb.ErrEncode) {
		t.Errorf("thiếu SOPClassUID: %v", err)
	}
	_, _, err = dicomweb.BuildPart10(dicomweb.Dataset{"00080016": {VR: "UI", Value: []any{"1.2."}}, "00080018": {VR: "UI", Value: []any{"1.2"}}}, nil)
	if !errors.Is(err, dicomweb.ErrEncode) {
		t.Errorf("UID sai: %v", err)
	}
}
