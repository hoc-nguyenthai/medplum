package fhirstore

import (
	"context"
	"strings"

	"trivita-fhir/pkg/fhirrouter"
)

// likeEscape thoát %, _ và \ để dùng với LIKE ... ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// QueryConcepts dịch điều kiện thành SQL. is-a và descendent-of dùng truy vấn đệ quy (UNION nên chu trình dừng được).
func (s *Store) QueryConcepts(ctx context.Context, sid string, q fhirrouter.ConceptQuery) ([]fhirrouter.Concept, int, error) {
	b := &qb{d: s.d}
	b.w(" FROM fhir_coding c WHERE c.system_id = " + b.arg(sid))
	if len(q.Codes) > 0 {
		ph := make([]string, len(q.Codes))
		for i, c := range q.Codes {
			ph[i] = b.arg(c)
		}
		b.w(" AND c.code IN (" + strings.Join(ph, ", ") + ")")
	}
	for _, f := range q.Filters {
		switch {
		case f.Op == "is-a" || f.Op == "descendent-of":
			if q.HierarchyProperty == "" {
				return nil, 0, fhirrouter.Unprocessable("CodeSystem không có thuộc tính phân cấp cho filter " + f.Op)
			}
			root, sys, prop := b.arg(f.Value), b.arg(sid), b.arg(q.HierarchyProperty)
			// Bước đệ quy phải DUYỆT TỪ d RỒI tra chỉ mục (system_id, property, value) cho từng nút. Không ép thứ tự thì khi bảng chưa có thống kê (vừa nạp xong, trước khi autovacuum chạy)
			// PostgreSQL chọn quét cả tập thuộc tính cho mỗi vòng (đo: 13 giây cho 20 000 concept), và SQLite cũng quét toàn bộ: LATERAL kèm OFFSET 0 (PostgreSQL: không có OFFSET 0 thì planner kéo subquery lên và vẫn chọn sai, đã đo) ép đúng thứ tự; SQLite cần cả CROSS JOIN (giữ thứ tự) lẫn INDEXED BY (nếu không nó dùng chỉ mục khóa chính chỉ theo system_id và quét cả tập: 18 giây cho 20 000 concept, đo bằng sqlite3).
			var step string
			if s.d.pg {
				step = "SELECT p.code FROM d, LATERAL (SELECT x.code FROM fhir_coding_property x WHERE x.system_id = " + sys + " AND x.property = " + prop + " AND x.value = d.code OFFSET 0) p"
			} else {
				step = "SELECT p.code FROM d CROSS JOIN fhir_coding_property p INDEXED BY fhir_coding_property_target WHERE p.system_id = " + sys + " AND p.property = " + prop + " AND p.value = d.code"
			}
			b.w(" AND c.code IN (WITH RECURSIVE d(code) AS (SELECT CAST(" + root + " AS text) UNION " + step + ") SELECT code FROM d)")
			if f.Op == "descendent-of" {
				b.w(" AND c.code <> " + b.arg(f.Value))
			}
		case f.Op == "=" && (f.Property == "code" || f.Property == "concept"):
			b.w(" AND c.code = " + b.arg(f.Value))
		case f.Op == "=":
			// IN (subquery không tương quan) thay cho EXISTS tương quan: SQLite không đảo được EXISTS để đi từ chỉ mục (system_id, property, value) nên quét cả bảng concept cho mỗi lần tra
			idx := ""
			if !s.d.pg {
				idx = " INDEXED BY fhir_coding_property_target" // như trên: SQLite chọn nhầm chỉ mục khóa chính
			}
			b.w(" AND c.code IN (SELECT p.code FROM fhir_coding_property p" + idx + " WHERE p.system_id = " + b.arg(sid) + " AND p.property = " +
				b.arg(f.Property) + " AND p.value = " + b.arg(f.Value) + ")")
		default:
			return nil, 0, fhirrouter.Unprocessable("filter không hỗ trợ trên kho concept: " + f.Property + " " + f.Op)
		}
	}
	if q.Text != "" {
		like := "%" + likeEscape(strings.ToLower(q.Text)) + "%"
		b.w(" AND (lower(c.code) LIKE " + b.arg(like) + " ESCAPE '\\' OR lower(c.display) LIKE " + b.arg(like) + " ESCAPE '\\')")
	}
	where := b.sb.String()
	args := b.args

	var total int
	if err := s.q().QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&total); err != nil {
		return nil, 0, internal(err)
	}
	sqlText := "SELECT c.code, c.display" + where + " ORDER BY c.code"
	if q.Limit > 0 {
		b.args = args
		sqlText += " LIMIT " + b.arg(q.Limit)
		args = b.args
	}
	if q.Offset > 0 {
		b.args = args
		if q.Limit == 0 {
			sqlText += " LIMIT -1"
			if s.d.pg {
				sqlText = strings.TrimSuffix(sqlText, " LIMIT -1")
			}
		}
		sqlText += " OFFSET " + b.arg(q.Offset)
		args = b.args
	}
	rows, err := s.q().QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, internal(err)
	}
	defer rows.Close()
	var out []fhirrouter.Concept
	for rows.Next() {
		var c fhirrouter.Concept
		if err := rows.Scan(&c.Code, &c.Display); err != nil {
			return nil, 0, internal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, internal(err)
	}
	return out, total, nil
}
