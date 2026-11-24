package fhirstore

import (
	"context"

	"trivita-fhir/pkg/fhirrouter"
)

// Cài MappingStore trên bảng fhir_concept_mapping (migration 00003).

var _ fhirrouter.MappingStore = (*Store)(nil)

func (s *Store) PutMappings(ctx context.Context, id string, ms []fhirrouter.StoredMapping) error {
	return s.inTx(ctx, func(q querier) error {
		for _, m := range ms {
			b := &qb{d: s.d}
			_, err := q.ExecContext(ctx, "INSERT INTO fhir_concept_mapping (map_id, source_system, source_code, source_display, target_system,"+
				" target_code, target_display, equivalence, comment, attributes) VALUES ("+
				b.arg(id)+", "+b.arg(m.SourceSystem)+", "+b.arg(m.SourceCode)+", "+b.arg(m.SourceDisplay)+", "+b.arg(m.TargetSystem)+", "+
				b.arg(m.TargetCode)+", "+b.arg(m.TargetDisplay)+", "+b.arg(m.Equivalence)+", "+b.arg(m.Comment)+", "+b.arg(m.Attributes)+
				") ON CONFLICT (map_id, source_system, source_code, target_system, target_code) DO UPDATE SET"+
				" source_display = excluded.source_display, target_display = excluded.target_display,"+
				" equivalence = excluded.equivalence, comment = excluded.comment, attributes = excluded.attributes", b.args...)
			if err != nil {
				return internal(err)
			}
		}
		return nil
	})
}

func (s *Store) FindMappings(ctx context.Context, id string, q fhirrouter.MappingQuery) ([]fhirrouter.StoredMapping, error) {
	b := &qb{d: s.d}
	b.w("SELECT source_system, source_code, source_display, target_system, target_code, target_display, equivalence, comment, attributes" +
		" FROM fhir_concept_mapping WHERE map_id = " + b.arg(id))
	for _, c := range []struct{ col, v string }{
		{"source_system", q.SourceSystem}, {"source_code", q.SourceCode}, {"target_system", q.TargetSystem}, {"target_code", q.TargetCode},
	} {
		if c.v != "" {
			b.w(" AND " + c.col + " = " + b.arg(c.v))
		}
	}
	b.w(" ORDER BY source_system, source_code, target_system, target_code")
	if q.Limit > 0 {
		b.w(" LIMIT " + b.arg(q.Limit))
	}
	rows, err := s.q().QueryContext(ctx, b.sb.String(), b.args...)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	var out []fhirrouter.StoredMapping
	for rows.Next() {
		var m fhirrouter.StoredMapping
		if err := rows.Scan(&m.SourceSystem, &m.SourceCode, &m.SourceDisplay, &m.TargetSystem, &m.TargetCode, &m.TargetDisplay, &m.Equivalence, &m.Comment, &m.Attributes); err != nil {
			return nil, internal(err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	return out, nil
}

func (s *Store) DeleteMappings(ctx context.Context, id string) (int, error) {
	n := 0
	err := s.inTx(ctx, func(q querier) error {
		var err error
		n, err = s.deleteMappings(ctx, q, id)
		return err
	})
	return n, err
}

func (s *Store) deleteMappings(ctx context.Context, q querier, id string) (int, error) {
	b := &qb{d: s.d}
	res, err := q.ExecContext(ctx, "DELETE FROM fhir_concept_mapping WHERE map_id = "+b.arg(id), b.args...)
	if err != nil {
		return 0, internal(err)
	}
	c, _ := res.RowsAffected()
	return int(c), nil
}
