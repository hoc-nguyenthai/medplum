package fhirstore

import (
	"context"
	"database/sql"
	"errors"

	"trivita-fhir/pkg/fhirrouter"
)

// Cài ConceptStore trên các bảng fhir_coding, fhir_coding_property, fhir_coding_designation (migration 00002).

var _ fhirrouter.ConceptStore = (*Store)(nil)

// conceptBatch là số dòng mỗi câu INSERT nhiều dòng (5 tham số mỗi dòng: dưới giới hạn 32766 của SQLite và 65535 của PostgreSQL).
const conceptBatch = 500

// checkBatch là số mã mỗi lượt kiểm tồn tại (mỗi mã một tham số: dưới giới hạn 32766 của SQLite).
const checkBatch = 10000

// insertBatch chèn các dòng bằng câu INSERT nhiều dòng theo lô (một lần đi tới CSDL cho mỗi lô thay vì mỗi dòng). conflict là phần sau VALUES (ON CONFLICT ...).
func (s *Store) insertBatch(ctx context.Context, q querier, table, cols string, rows [][]any, conflict string) error {
	for from := 0; from < len(rows); from += conceptBatch {
		to := min(from+conceptBatch, len(rows))
		b := &qb{d: s.d}
		b.w("INSERT INTO " + table + " (" + cols + ") VALUES ")
		for i := from; i < to; i++ {
			if i > from {
				b.w(", ")
			}
			b.w("(")
			for j, v := range rows[i] {
				if j > 0 {
					b.w(", ")
				}
				b.w(b.arg(v))
			}
			b.w(")")
		}
		b.w(" " + conflict)
		if _, err := q.ExecContext(ctx, b.sb.String(), b.args...); err != nil {
			return internal(err)
		}
	}
	return nil
}

// dedupe bỏ dòng trùng khóa trong CÙNG một lô: PostgreSQL từ chối ON CONFLICT DO UPDATE chạm một dòng hai lần trong một câu lệnh. Dòng sau thắng (như chèn tuần tự).
func dedupe(rows [][]any, keyCols int) [][]any {
	idx := map[string]int{}
	out := make([][]any, 0, len(rows))
	for _, r := range rows {
		k := ""
		for _, v := range r[:keyCols] {
			k += v.(string) + "\x00"
		}
		if i, ok := idx[k]; ok {
			out[i] = r
			continue
		}
		idx[k] = len(out)
		out = append(out, r)
	}
	return out
}

func (s *Store) PutConcepts(ctx context.Context, sid string, cs []fhirrouter.Concept) error {
	rows := make([][]any, len(cs))
	for i, c := range cs {
		rows[i] = []any{sid, c.Code, c.Display}
	}
	rows = dedupe(rows, 2)
	return s.inTx(ctx, func(q querier) error {
		return s.insertBatch(ctx, q, "fhir_coding", "system_id, code, display", rows, "ON CONFLICT (system_id, code) DO UPDATE SET display = excluded.display")
	})
}

func (s *Store) PutConceptProperties(ctx context.Context, sid string, ps []fhirrouter.ConceptProperty) error {
	rows := make([][]any, len(ps))
	codes := make([]string, len(ps))
	for i, p := range ps {
		rows[i] = []any{sid, p.Code, p.Property, p.Type, p.Value}
		codes[i] = p.Code
	}
	return s.inTx(ctx, func(q querier) error {
		if err := s.requireCodes(ctx, q, sid, codes); err != nil {
			return err
		}
		return s.insertBatch(ctx, q, "fhir_coding_property", "system_id, code, property, type, value", rows, "ON CONFLICT DO NOTHING")
	})
}

func (s *Store) PutConceptDesignations(ctx context.Context, sid string, ds []fhirrouter.ConceptDesignation) error {
	rows := make([][]any, len(ds))
	codes := make([]string, len(ds))
	for i, d := range ds {
		rows[i] = []any{sid, d.Code, d.Language, d.Value}
		codes[i] = d.Code
	}
	return s.inTx(ctx, func(q querier) error {
		if err := s.requireCodes(ctx, q, sid, codes); err != nil {
			return err
		}
		return s.insertBatch(ctx, q, "fhir_coding_designation", "system_id, code, language, value", rows, "ON CONFLICT DO NOTHING")
	})
}

// requireCodes kiểm mọi mã có trong fhir_coding của CodeSystem (theo lô); báo mã thiếu ĐẦU TIÊN theo thứ tự đầu vào, như kiểm từng dòng.
func (s *Store) requireCodes(ctx context.Context, q querier, sid string, codes []string) error {
	seen := map[string]bool{}
	var uniq []string
	for _, c := range codes {
		if !seen[c] {
			seen[c] = true
			uniq = append(uniq, c)
		}
	}
	have := map[string]bool{}
	for from := 0; from < len(uniq); from += checkBatch {
		part := uniq[from:min(from+checkBatch, len(uniq))]
		// Nối với danh sách mã bằng JOIN thay cho `code IN (...)`: với danh sách tham số PostgreSQL (chưa có thống kê) chọn quét cả bảng và so từng dòng với từng mã trong danh sách
		// (đo: 0,86 giây mỗi 500 mã ở 60 000 concept, chậm dần theo cỡ bảng), còn JOIN luôn là một lượt băm hoặc tra chỉ mục. Một lượt cho cả lô lớn thay vì nhiều lượt nhỏ.
		b := &qb{d: s.d}
		b.w("WITH v(code) AS (VALUES ")
		for i, c := range part {
			if i > 0 {
				b.w(", ")
			}
			b.w("(CAST(" + b.arg(c) + " AS text))")
		}
		b.w(") SELECT c.code FROM v, fhir_coding c WHERE c.code = v.code AND c.system_id = " + b.arg(sid))
		rows, err := q.QueryContext(ctx, b.sb.String(), b.args...)
		if err != nil {
			return internal(err)
		}
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				_ = rows.Close()
				return internal(err)
			}
			have[c] = true
		}
		if err := rows.Close(); err != nil {
			return internal(err)
		}
	}
	for _, c := range codes {
		if !have[c] {
			return fhirrouter.BadRequest("Concept chưa có: " + c)
		}
	}
	return nil
}

func (s *Store) requireCode(ctx context.Context, q querier, sid, code string) error {
	b := &qb{d: s.d}
	var n int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM fhir_coding WHERE system_id = "+b.arg(sid)+" AND code = "+b.arg(code), b.args...).Scan(&n); err != nil {
		return internal(err)
	}
	if n == 0 {
		return fhirrouter.BadRequest("Concept chưa có: " + code)
	}
	return nil
}

func (s *Store) GetConcept(ctx context.Context, sid, code string) (*fhirrouter.StoredConcept, error) {
	q := s.q()
	b := &qb{d: s.d}
	out := &fhirrouter.StoredConcept{Code: code}
	err := q.QueryRowContext(ctx, "SELECT display FROM fhir_coding WHERE system_id = "+b.arg(sid)+" AND code = "+b.arg(code), b.args...).Scan(&out.Display)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, internal(err)
	}
	b = &qb{d: s.d}
	rows, err := q.QueryContext(ctx, "SELECT property, type, value FROM fhir_coding_property WHERE system_id = "+b.arg(sid)+" AND code = "+b.arg(code)+" ORDER BY property, value", b.args...)
	if err != nil {
		return nil, internal(err)
	}
	for rows.Next() {
		p := fhirrouter.ConceptProperty{Code: code}
		if err := rows.Scan(&p.Property, &p.Type, &p.Value); err != nil {
			_ = rows.Close()
			return nil, internal(err)
		}
		out.Properties = append(out.Properties, p)
	}
	if err := rows.Close(); err != nil {
		return nil, internal(err)
	}
	b = &qb{d: s.d}
	rows, err = q.QueryContext(ctx, "SELECT language, value FROM fhir_coding_designation WHERE system_id = "+b.arg(sid)+" AND code = "+b.arg(code)+" ORDER BY language, value", b.args...)
	if err != nil {
		return nil, internal(err)
	}
	for rows.Next() {
		d := fhirrouter.ConceptDesignation{Code: code}
		if err := rows.Scan(&d.Language, &d.Value); err != nil {
			_ = rows.Close()
			return nil, internal(err)
		}
		out.Designations = append(out.Designations, d)
	}
	if err := rows.Close(); err != nil {
		return nil, internal(err)
	}
	return out, nil
}

func (s *Store) DeleteConcepts(ctx context.Context, sid string) (int, error) {
	n := 0
	err := s.inTx(ctx, func(q querier) error {
		var err error
		n, err = s.deleteConcepts(ctx, q, sid)
		return err
	})
	return n, err
}

func (s *Store) deleteConcepts(ctx context.Context, q querier, sid string) (int, error) {
	n := 0
	for _, t := range []string{"fhir_coding_designation", "fhir_coding_property", "fhir_coding"} {
		b := &qb{d: s.d}
		res, err := q.ExecContext(ctx, "DELETE FROM "+t+" WHERE system_id = "+b.arg(sid), b.args...)
		if err != nil {
			return 0, internal(err)
		}
		if t == "fhir_coding" {
			c, _ := res.RowsAffected()
			n = int(c)
		}
	}
	return n, nil
}
