package s3store

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/storetest"
)

// Bộ test hợp đồng chạy với một máy chủ S3 THẬT (MinIO, RustFS, AWS...) thay cho máy chủ giả: kiểm chữ ký SigV4 do máy chủ thật xác minh, Range, danh sách, xóa, mã lỗi.
// Chỉ chạy khi đặt FHIR_S3_TEST_ENDPOINT (ví dụ http://127.0.0.1:59000), FHIR_S3_TEST_BUCKET (đã tồn tại), FHIR_S3_TEST_ACCESS_KEY_ID, FHIR_S3_TEST_SECRET_ACCESS_KEY,
// tùy chọn FHIR_S3_TEST_REGION (mặc định us-east-1). Mỗi test dùng tiền tố ngẫu nhiên riêng nên chạy song song hay lặp lại đều an toàn.
func realStore(t testing.TB) binarystore.Store {
	t.Helper()
	endpoint := os.Getenv("FHIR_S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("không đặt FHIR_S3_TEST_ENDPOINT")
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	region := os.Getenv("FHIR_S3_TEST_REGION")
	if region == "" {
		region = "us-east-1"
	}
	st, err := New(Config{
		Bucket: os.Getenv("FHIR_S3_TEST_BUCKET"), Prefix: "contract-" + hex.EncodeToString(rnd[:]) + "/", Region: region, Endpoint: endpoint,
		AccessKeyID: os.Getenv("FHIR_S3_TEST_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("FHIR_S3_TEST_SECRET_ACCESS_KEY"), TempDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRealS3Contract(t *testing.T) {
	storetest.Run(t, func(t testing.TB) binarystore.Store { return realStore(t) })
}

func TestRealS3Range(t *testing.T) {
	storetest.RunRange(t, func(t testing.TB) binarystore.Store { return realStore(t) })
}

func TestRealS3Lister(t *testing.T) {
	storetest.RunLister(t, func(t testing.TB) binarystore.Store { return realStore(t) })
}
