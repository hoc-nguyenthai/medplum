package fhirstore_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirstore"
)

func eachStore(t *testing.T, fn func(t *testing.T, s *fhirstore.Store)) {
	t.Run("sqlite", func(t *testing.T) { fn(t, newSQLite(t)) })
	if os.Getenv(PostgresEnv) != "" {
		t.Run("postgres", func(t *testing.T) { fn(t, newPostgres(t)) })
	}
}

func code(i int) string { return fmt.Sprintf("C%07d", i) }

// Nạp theo lô: vượt cỡ lô (500), trùng khóa trong cùng một lô (dòng sau thắng, PostgreSQL không cho một câu lệnh chạm một dòng hai lần), ghi đè display, thiếu mã báo mã thiếu ĐẦU TIÊN.
func TestConceptBatchInsertSemantics(t *testing.T) {
	eachStore(t, func(t *testing.T, s *fhirstore.Store) {
		ctx := context.Background()
		var cs []fhirrouter.Concept
		for i := 0; i < 1300; i++ {
			cs = append(cs, fhirrouter.Concept{Code: code(i), Display: fmt.Sprint("d", i)})
		}
		cs = append(cs, fhirrouter.Concept{Code: code(5), Display: "đè lần hai"}) // trùng trong cùng lô
		if err := s.PutConcepts(ctx, "sys", cs); err != nil {
			t.Fatal(err)
		}
		got, total, err := s.QueryConcepts(ctx, "sys", fhirrouter.ConceptQuery{})
		if err != nil || total != 1300 || len(got) != 1300 {
			t.Fatalf("tổng %d, %d dòng, %v", total, len(got), err)
		}
		if c, _ := s.GetConcept(ctx, "sys", code(5)); c == nil || c.Display != "đè lần hai" {
			t.Errorf("dòng sau phải thắng: %+v", c)
		}
		// nạp lại: ghi đè display, không thêm dòng
		if err := s.PutConcepts(ctx, "sys", []fhirrouter.Concept{{Code: code(7), Display: "mới"}}); err != nil {
			t.Fatal(err)
		}
		if c, _ := s.GetConcept(ctx, "sys", code(7)); c.Display != "mới" {
			t.Errorf("ghi đè display: %+v", c)
		}
		// thuộc tính: nhiều hơn một lô, trùng thì bỏ qua, thiếu mã thì lỗi và báo mã thiếu đầu tiên theo thứ tự đầu vào
		var ps []fhirrouter.ConceptProperty
		for i := 1; i < 1300; i++ {
			ps = append(ps, fhirrouter.ConceptProperty{Code: code(i), Property: "parent", Type: "code", Value: code((i - 1) / 10)})
		}
		ps = append(ps, ps[0]) // trùng hẳn: ON CONFLICT DO NOTHING
		if err := s.PutConceptProperties(ctx, "sys", ps); err != nil {
			t.Fatal(err)
		}
		bad := []fhirrouter.ConceptProperty{{Code: code(1), Property: "k", Type: "string", Value: "v"}, {Code: "KHONGCO2", Property: "k", Type: "string", Value: "v"}, {Code: "KHONGCO1", Property: "k", Type: "string", Value: "v"}}
		err = s.PutConceptProperties(ctx, "sys", bad)
		if err == nil || !strings.Contains(err.Error(), "KHONGCO2") {
			t.Errorf("phải báo mã thiếu đầu tiên (KHONGCO2): %v", err)
		}
		if c, _ := s.GetConcept(ctx, "sys", code(1)); len(c.Properties) != 1 {
			t.Errorf("giao dịch lỗi phải hoàn tác, concept 1 có %d thuộc tính", len(c.Properties))
		}
		ds := []fhirrouter.ConceptDesignation{{Code: code(2), Language: "vi", Value: "hai"}, {Code: code(2), Language: "vi", Value: "hai"}}
		if err := s.PutConceptDesignations(ctx, "sys", ds); err != nil {
			t.Fatal(err)
		}
		if c, _ := s.GetConcept(ctx, "sys", code(2)); len(c.Designations) != 1 {
			t.Errorf("designation trùng: %+v", c.Designations)
		}
		if err := s.PutConceptDesignations(ctx, "sys", []fhirrouter.ConceptDesignation{{Code: "KHONGCO", Value: "x"}}); err == nil {
			t.Error("designation của mã chưa có phải lỗi")
		}
	})
}

// Truy vấn cây trên bảng LỚN và CHƯA CÓ THỐNG KÊ (ngay sau khi nạp, trước khi autovacuum/ANALYZE chạy) phải đi từ chỉ mục (system_id, property, value) từng nút: dạng JOIN thường làm PostgreSQL
// quét cả tập thuộc tính cho mỗi vòng (13 giây) và SQLite chọn nhầm chỉ mục khóa chính (66 giây cho is-a ở 20 000 concept). Ngưỡng 10 giây rộng hơn 100 lần so với bình thường (vài chục ms)
// nên không chập chờn, mà vẫn đỏ ngay khi quay lại dạng cũ.
func TestConceptTreeQueriesAreFastOnLargeFreshTables(t *testing.T) {
	if testing.Short() {
		t.Skip("20 000 concept")
	}
	eachStore(t, func(t *testing.T, s *fhirstore.Store) {
		ctx := context.Background()
		const n = 20000
		var cs []fhirrouter.Concept
		var ps []fhirrouter.ConceptProperty
		for i := 0; i < n; i++ {
			cs = append(cs, fhirrouter.Concept{Code: code(i), Display: fmt.Sprint("Concept ", i)})
			if i > 0 {
				ps = append(ps, fhirrouter.ConceptProperty{Code: code(i), Property: "parent", Type: "code", Value: code((i - 1) / 10)})
			}
		}
		if err := s.PutConcepts(ctx, "sys", cs); err != nil {
			t.Fatal(err)
		}
		if err := s.PutConceptProperties(ctx, "sys", ps); err != nil {
			t.Fatal(err)
		}
		timed := func(name string, q fhirrouter.ConceptQuery, wantTotal int) {
			t0 := time.Now()
			_, total, err := s.QueryConcepts(ctx, "sys", q)
			if d := time.Since(t0); err != nil || total != wantTotal || d > 10*time.Second {
				t.Errorf("%s: tổng %d (muốn %d), %v, %v (ngưỡng 10 giây)", name, total, wantTotal, err, d)
			}
		}
		// số cháu chắt của một nút tính bằng cách đi cây trong bộ nhớ (cha của i là (i-1)/10), để đối chiếu với kết quả SQL
		var subtree func(root int) int
		subtree = func(root int) int {
			total := 1
			for c := root*10 + 1; c <= root*10+10 && c < n; c++ {
				total += subtree(c)
			}
			return total
		}
		h := "parent"
		timed("is-a gốc cả cây", fhirrouter.ConceptQuery{HierarchyProperty: h, Filters: []fhirrouter.ConceptFilter{{Op: "is-a", Value: code(0)}}, Limit: 100001}, n)
		timed("is-a C1", fhirrouter.ConceptQuery{HierarchyProperty: h, Filters: []fhirrouter.ConceptFilter{{Op: "is-a", Value: code(1)}}, Limit: 100001}, subtree(1))
		timed("descendent-of C1999", fhirrouter.ConceptQuery{HierarchyProperty: h, Filters: []fhirrouter.ConceptFilter{{Op: "descendent-of", Value: code(1999)}}}, subtree(1999)-1)
		timed("con trực tiếp (= parent)", fhirrouter.ConceptQuery{Filters: []fhirrouter.ConceptFilter{{Op: "=", Property: h, Value: code(5)}}}, 10)
	})
}

// Kiểm tồn tại mã theo lô vượt cỡ lô kiểm (10 000): mã thiếu nằm ở lô thứ hai vẫn bị bắt.
func TestConceptExistenceCheckSpansBatches(t *testing.T) {
	eachStore(t, func(t *testing.T, s *fhirstore.Store) {
		ctx := context.Background()
		var cs []fhirrouter.Concept
		var ps []fhirrouter.ConceptProperty
		for i := 0; i < 10500; i++ {
			cs = append(cs, fhirrouter.Concept{Code: code(i), Display: "x"})
			ps = append(ps, fhirrouter.ConceptProperty{Code: code(i), Property: "k", Type: "string", Value: "v"})
		}
		if err := s.PutConcepts(ctx, "sys", cs); err != nil {
			t.Fatal(err)
		}
		if err := s.PutConceptProperties(ctx, "sys", ps); err != nil {
			t.Fatal(err)
		}
		ps = append(ps, fhirrouter.ConceptProperty{Code: "THIEU-O-LO-HAI", Property: "k", Type: "string", Value: "v"})
		if err := s.PutConceptProperties(ctx, "sys", ps); err == nil || !strings.Contains(err.Error(), "THIEU-O-LO-HAI") {
			t.Errorf("mã thiếu ở lô thứ hai phải bị bắt: %v", err)
		}
		// khác CodeSystem thì không tính là có
		if err := s.PutConceptProperties(ctx, "khac", ps[:1]); err == nil {
			t.Error("mã của CodeSystem khác không được coi là có")
		}
	})
}
