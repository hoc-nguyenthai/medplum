package fhirstore

import (
	"context"
	"database/sql"
	"errors"

	"trivita-fhir/pkg/fhirrouter"
)

// Cài ConceptStore trên các bảng fhir_coding, fhir_coding_property, fhir_coding_designation (migration 00002).

var _ fhirrouter.ConceptStore = (*Store)(nil)

func (s *Store) PutConcepts(ctx context.Context, sid string, cs []fhirrouter.Concept) error {
	return s.inTx(ctx, func(q querier) error {
		for _, c := range cs {
			b := &qb{d: s.d}
			_, err := q.ExecContext(ctx, "INSERT INTO fhir_coding (system_id, code, display) VALUES ("+
				b.arg(sid)+", "+b.arg(c.Code)+", "+b.arg(c.Display)+
				") ON CONFLICT (system_id, code) DO UPDATE SET display = excluded.display", b.args...)
			if err != nil {
				return internal(err)
			}
		}
		return nil
	})
}

func (s *Store) PutConceptProperties(ctx context.Context, sid string, ps []fhirrouter.ConceptProperty) error {
	return s.inTx(ctx, func(q querier) error {
		for _, p := range ps {
			if err := s.requireCode(ctx, q, sid, p.Code); err != nil {
				return err
			}
			b := &qb{d: s.d}
			_, err := q.ExecContext(ctx, "INSERT INTO fhir_coding_property (system_id, code, property, type, value) VALUES ("+
				b.arg(sid)+", "+b.arg(p.Code)+", "+b.arg(p.Property)+", "+b.arg(p.Type)+", "+b.arg(p.Value)+
				") ON CONFLICT DO NOTHING", b.args...)
			if err != nil {
				return internal(err)
			}
		}
		return nil
	})
}

func (s *Store) PutConceptDesignations(ctx context.Context, sid string, ds []fhirrouter.ConceptDesignation) error {
	return s.inTx(ctx, func(q querier) error {
		for _, d := range ds {
			if err := s.requireCode(ctx, q, sid, d.Code); err != nil {
				return err
			}
			b := &qb{d: s.d}
			_, err := q.ExecContext(ctx, "INSERT INTO fhir_coding_designation (system_id, code, language, value) VALUES ("+
				b.arg(sid)+", "+b.arg(d.Code)+", "+b.arg(d.Language)+", "+b.arg(d.Value)+
				") ON CONFLICT DO NOTHING", b.args...)
			if err != nil {
				return internal(err)
			}
		}
		return nil
	})
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
