package s3store

import (
	"testing"

	"trivita-fhir/pkg/binarystore"
	"trivita-fhir/pkg/binarystore/storetest"
)

func TestS3RangeContract(t *testing.T) {
	storetest.RunRange(t, func(t testing.TB) binarystore.Store { return newFakeS3(t.(*testing.T)).store(t.(*testing.T), nil) })
}

// Máy chủ bỏ qua Range và trả 200 với cả nội dung: kho vẫn trả đúng byte (không bao giờ sai dữ liệu vì proxy không hỗ trợ Range).
func TestS3RangeFallsBackWhenServerIgnoresRange(t *testing.T) {
	f := newFakeS3(t)
	f.ignoreRange = true
	storetest.RunRange(t, func(tb testing.TB) binarystore.Store { return f.store(t, nil) })
}
