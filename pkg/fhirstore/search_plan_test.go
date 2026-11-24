package fhirstore

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
)

// Tìm kiếm theo tham số phải đi từ chỉ mục tìm kiếm (fhir_idx_*) rồi tra resource theo khóa chính, không quét cả loại rồi dò chỉ mục cho từng dòng: dạng EXISTS tương quan
// làm SQLite quét O(số resource của loại) cho mỗi truy vấn (đo ở load test của dicomweb: 16,7 ms mỗi truy vấn với 20 000 instance và làm dịch vụ gần như đứng ở cỡ vài
// trăm nghìn; riêng điều kiện phủ định (:not) vốn phải duyệt cả loại nên không nằm trong test). Test kiểm kế hoạch thực thi chứ không đo thời gian nên không chập chờn.
func TestSearchUsesTheSearchIndexNotAScanOfTheType(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{URL: "sqlite://:memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.CreateResource(ctx, &fhir.DicomInstance{ID: id, SopInstanceUid: "1.2." + id, Series: &fhir.Reference{Reference: "DicomSeries/s1"}}); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		rt string
		q  url.Values
	}{
		"chuỗi exact":     {"DicomInstance", url.Values{"sop-instance-uid:exact": {"1.2.a"}}},
		"tham chiếu":      {"DicomInstance", url.Values{"series": {"DicomSeries/s1"}}},
		"hai điều kiện":   {"DicomInstance", url.Values{"series": {"DicomSeries/s1"}, "sop-instance-uid:exact": {"1.2.a"}}},
		"thiếu giá trị":   {"DicomInstance", url.Values{"sop-instance-uid:missing": {"false"}}},
		"token đúng loại": {"Patient", url.Values{"gender": {"male"}}},
	} {
		req, err := fhirsearch.Parse(c.rt, c.q)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b := &qb{d: s.d}
		where, err := s.whereClause(b, req, []string{c.rt})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rows, err := db.SQL.QueryContext(ctx, "EXPLAIN QUERY PLAN SELECT r.content FROM fhir_resource r WHERE "+where, b.args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, " | ")
		// resource phải được tra theo khóa chính (loại và id), không phải duyệt theo loại; và không có truy vấn con tương quan
		if !strings.Contains(joined, "(resource_type=? AND id=?)") || strings.Contains(joined, "CORRELATED") {
			t.Errorf("%s: kế hoạch thực thi:\n  %s", name, strings.Join(plan, "\n  "))
		}
	}
}
