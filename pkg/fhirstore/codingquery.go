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
			b.w(" AND c.code IN (WITH RECURSIVE d(code) AS (SELECT CAST(" + root + " AS text) UNION SELECT p.code FROM d, fhir_coding_property p" +
				" WHERE p.value = d.code AND p.system_id = " + sys + " AND p.property = " + prop + ") SELECT code FROM d)")
			if f.Op == "descendent-of" {
				b.w(" AND c.code <> " + b.arg(f.Value))
			}
		case f.Op == "=" && (f.Property == "code" || f.Property == "concept"):
			b.w(" AND c.code = " + b.arg(f.Value))
		case f.Op == "=":
			b.w(" AND EXISTS (SELECT 1 FROM fhir_coding_property p WHERE p.system_id = c.system_id AND p.code = c.code AND p.property = " +
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
