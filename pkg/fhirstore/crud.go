package fhirstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// ---------------------------------------------------------------- chuyển đổi

// docOf chuyển resource thành cây JSON (giữ nguyên số).
func docOf(r fhir.Resource) (map[string]any, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return decodeDoc(raw)
}

func decodeDoc(raw []byte) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func resourceOf(content []byte) (fhir.Resource, error) {
	r, err := fhir.UnmarshalResource(content)
	if err != nil {
		return nil, fhirrouter.Internal("dữ liệu lưu trữ không hợp lệ: " + err.Error())
	}
	return r, nil
}

// stampDoc gán id và meta (versionId, lastUpdated) cho doc.
func (s *Store) stampDoc(doc map[string]any, id string) (vid, lu string) {
	doc["id"] = id
	meta, _ := doc["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	vid, lu = newID(), s.stamp()
	meta["versionId"], meta["lastUpdated"] = vid, lu
	doc["meta"] = meta
	return vid, lu
}

func marshalDoc(doc map[string]any) ([]byte, error) { return json.Marshal(doc) }

func internal(err error) error {
	var fe *fhirrouter.Error
	if errors.As(err, &fe) {
		return err
	}
	return fhirrouter.Internal(err.Error())
}

// ---------------------------------------------------------------- tạo, sửa, xóa

func (s *Store) CreateResource(ctx context.Context, r fhir.Resource) (fhir.Resource, error) {
	doc, err := docOf(r)
	if err != nil {
		return nil, internal(err)
	}
	rt, id := r.GetResourceType(), r.GetID()
	if id == "" {
		id = newID()
	}
	vid, lu := s.stampDoc(doc, id)
	content, err := marshalDoc(doc)
	if err != nil {
		return nil, internal(err)
	}
	err = s.inTx(ctx, func(q querier) error {
		res, err := q.ExecContext(ctx, s.insertResource(), rt, id, vid, lu, false, string(content))
		if err != nil {
			return internal(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fhirrouter.Conflict("Resource đã tồn tại: " + rt + "/" + id)
		}
		if err := s.insertHistory(ctx, q, rt, id, vid, lu, "POST", false, content); err != nil {
			return err
		}
		return s.reindex(ctx, q, rt, id, doc)
	})
	if err != nil {
		return nil, err
	}
	return resourceOf(content)
}

func (s *Store) insertResource() string {
	b := &qb{d: s.d}
	vals := []string{b.arg(nil), b.arg(nil), b.arg(nil), b.arg(nil), b.arg(nil), s.d.jsonParam(b.arg(nil))}
	return "INSERT INTO fhir_resource (resource_type, id, version_id, last_updated, deleted, content) VALUES (" +
		strings.Join(vals, ", ") + ") ON CONFLICT (resource_type, id) DO NOTHING"
}

func (s *Store) insertHistory(ctx context.Context, q querier, rt, id, vid, lu, method string, deleted bool, content []byte) error {
	b := &qb{d: s.d}
	vals := []string{b.arg(rt), b.arg(id), b.arg(vid), b.arg(lu), b.arg(method), b.arg(deleted), s.d.jsonParam(b.arg(string(content)))}
	_, err := q.ExecContext(ctx, "INSERT INTO fhir_history (resource_type, id, version_id, last_updated, method, deleted, content) VALUES ("+
		strings.Join(vals, ", ")+")", b.args...)
	if err != nil {
		return internal(err)
	}
	return nil
}

// current đọc phiên bản hiện tại (khóa dòng trên PostgreSQL); NotFound/Gone nếu không dùng được.
func (s *Store) current(ctx context.Context, q querier, rt, id string, lock bool) (vid string, err error) {
	b := &qb{d: s.d}
	query := "SELECT version_id, deleted FROM fhir_resource WHERE resource_type = " + b.arg(rt) + " AND id = " + b.arg(id)
	if lock && s.d.pg {
		query += " FOR UPDATE"
	}
	var deleted bool
	if err := q.QueryRowContext(ctx, query, b.args...).Scan(&vid, &deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fhirrouter.NotFound()
		}
		return "", internal(err)
	}
	if deleted {
		return "", fhirrouter.Gone()
	}
	return vid, nil
}

func (s *Store) UpdateResource(ctx context.Context, r fhir.Resource, opts fhirrouter.UpdateOptions) (fhir.Resource, error) {
	doc, err := docOf(r)
	if err != nil {
		return nil, internal(err)
	}
	rt, id := r.GetResourceType(), r.GetID()
	// meta do client gửi (versionId, lastUpdated) bị bỏ qua
	if meta, ok := doc["meta"].(map[string]any); ok {
		delete(meta, "versionId")
		delete(meta, "lastUpdated")
	}
	var content []byte
	err = s.inTx(ctx, func(q querier) error {
		cur, err := s.current(ctx, q, rt, id, true)
		if err != nil {
			return err
		}
		if opts.IfMatch != "" && opts.IfMatch != cur {
			return fhirrouter.PreconditionFailed("Phiên bản không khớp: hiện tại " + cur)
		}
		vid, lu := s.stampDoc(doc, id)
		if content, err = marshalDoc(doc); err != nil {
			return internal(err)
		}
		b := &qb{d: s.d}
		_, err = q.ExecContext(ctx, "UPDATE fhir_resource SET version_id = "+b.arg(vid)+", last_updated = "+b.arg(lu)+
			", deleted = "+b.arg(false)+", content = "+s.d.jsonParam(b.arg(string(content)))+
			" WHERE resource_type = "+b.arg(rt)+" AND id = "+b.arg(id), b.args...)
		if err != nil {
			return internal(err)
		}
		if err := s.insertHistory(ctx, q, rt, id, vid, lu, "PUT", false, content); err != nil {
			return err
		}
		return s.reindex(ctx, q, rt, id, doc)
	})
	if err != nil {
		return nil, err
	}
	return resourceOf(content)
}

func (s *Store) DeleteResource(ctx context.Context, rt, id string) error {
	return s.inTx(ctx, func(q querier) error {
		if _, err := s.current(ctx, q, rt, id, true); err != nil {
			return err
		}
		doc := map[string]any{"resourceType": rt}
		vid, lu := s.stampDoc(doc, id)
		content, err := marshalDoc(doc)
		if err != nil {
			return internal(err)
		}
		b := &qb{d: s.d}
		_, err = q.ExecContext(ctx, "UPDATE fhir_resource SET version_id = "+b.arg(vid)+", last_updated = "+b.arg(lu)+
			", deleted = "+b.arg(true)+", content = "+s.d.jsonParam(b.arg(string(content)))+
			" WHERE resource_type = "+b.arg(rt)+" AND id = "+b.arg(id), b.args...)
		if err != nil {
			return internal(err)
		}
		if err := s.insertHistory(ctx, q, rt, id, vid, lu, "DELETE", true, content); err != nil {
			return err
		}
		return s.deleteIndex(ctx, q, rt, id)
	})
}

// ---------------------------------------------------------------- đọc

func (s *Store) ReadResource(ctx context.Context, rt, id string) (fhir.Resource, error) {
	b := &qb{d: s.d}
	var content []byte
	var deleted bool
	err := s.q().QueryRowContext(ctx, "SELECT content, deleted FROM fhir_resource WHERE resource_type = "+b.arg(rt)+" AND id = "+b.arg(id), b.args...).
		Scan(&content, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fhirrouter.NotFound()
	}
	if err != nil {
		return nil, internal(err)
	}
	if deleted {
		return nil, fhirrouter.Gone()
	}
	return resourceOf(content)
}

func (s *Store) ReadVersion(ctx context.Context, rt, id, vid string) (fhir.Resource, error) {
	b := &qb{d: s.d}
	var content []byte
	var deleted bool
	err := s.q().QueryRowContext(ctx, "SELECT content, deleted FROM fhir_history WHERE resource_type = "+b.arg(rt)+
		" AND id = "+b.arg(id)+" AND version_id = "+b.arg(vid), b.args...).Scan(&content, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fhirrouter.NotFound()
	}
	if err != nil {
		return nil, internal(err)
	}
	if deleted {
		return nil, fhirrouter.Gone()
	}
	return resourceOf(content)
}

func (s *Store) ReadHistory(ctx context.Context, rt, id string) ([]fhirrouter.HistoryEntry, error) {
	b := &qb{d: s.d}
	rows, err := s.q().QueryContext(ctx, "SELECT content, method, version_id, last_updated, deleted FROM fhir_history WHERE resource_type = "+
		b.arg(rt)+" AND id = "+b.arg(id)+" ORDER BY seq DESC", b.args...)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	var out []fhirrouter.HistoryEntry
	for rows.Next() {
		var content []byte
		e := fhirrouter.HistoryEntry{ResourceType: rt, ID: id}
		if err := rows.Scan(&content, &e.Method, &e.VersionID, &e.LastUpdated, &e.Deleted); err != nil {
			return nil, internal(err)
		}
		if e.Resource, err = resourceOf(content); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	if len(out) == 0 {
		return nil, fhirrouter.NotFound()
	}
	return out, nil
}

// ---------------------------------------------------------------- chỉ mục

// indexTables là các bảng chỉ mục theo kiểu tham số.
var indexTables = []string{"string", "token", "reference", "date", "number", "quantity", "uri"}

func (s *Store) deleteIndex(ctx context.Context, q querier, rt, id string) error {
	for _, t := range indexTables {
		b := &qb{d: s.d}
		if _, err := q.ExecContext(ctx, "DELETE FROM fhir_idx_"+t+" WHERE resource_type = "+b.arg(rt)+" AND id = "+b.arg(id), b.args...); err != nil {
			return internal(err)
		}
	}
	return nil
}

var defsCache sync.Map // resourceType -> []fhir.SearchParamDef

// indexedParams trả về mỗi code một định nghĩa (đã áp dụng quy tắc ưu tiên của fhirsearch.Lookup),
// bỏ composite, special và _id (_id dùng thẳng cột id).
func indexedParams(rt string) []fhir.SearchParamDef {
	if v, ok := defsCache.Load(rt); ok {
		return v.([]fhir.SearchParamDef)
	}
	var out []fhir.SearchParamDef
	seen := map[string]bool{}
	for _, d := range fhir.SearchParams(rt) {
		if seen[d.Code] || d.Code == "_id" {
			continue
		}
		seen[d.Code] = true
		def, ok := fhirsearch.Lookup(rt, d.Code)
		if !ok || def.Type == "composite" || def.Type == "special" {
			continue
		}
		out = append(out, def)
	}
	defsCache.Store(rt, out)
	return out
}

// compositeParams trả về các tham số composite của rt (mỗi code một định nghĩa).
func compositeParams(rt string) []fhir.SearchParamDef {
	if v, ok := compositeCache.Load(rt); ok {
		return v.([]fhir.SearchParamDef)
	}
	var out []fhir.SearchParamDef
	seen := map[string]bool{}
	for _, d := range fhir.SearchParams(rt) {
		if seen[d.Code] || d.Type != "composite" {
			continue
		}
		seen[d.Code] = true
		if def, ok := fhirsearch.Lookup(rt, d.Code); ok && def.Type == "composite" {
			out = append(out, def)
		}
	}
	compositeCache.Store(rt, out)
	return out
}

var compositeCache sync.Map // resourceType -> []fhir.SearchParamDef

type rowSet struct {
	cols []string
	rows [][]any
}

// reindex xóa rồi ghi lại toàn bộ chỉ mục của resource.
func (s *Store) reindex(ctx context.Context, q querier, rt, id string, doc map[string]any) error {
	if err := s.deleteIndex(ctx, q, rt, id); err != nil {
		return err
	}
	sets := map[string]*rowSet{
		"string":    {cols: []string{"raw", "norm"}},
		"token":     {cols: []string{"kind", "system", "value"}},
		"reference": {cols: []string{"raw", "target", "ref_type", "ref_id"}},
		"date":      {cols: []string{"lo", "hi"}},
		"number":    {cols: []string{"value"}},
		"quantity":  {cols: []string{"value", "system", "qcode", "unit"}},
		"uri":       {cols: []string{"value", "norm"}},
	}
	add := func(table, code string, ord int, vals ...any) {
		rs := sets[table]
		rs.rows = append(rs.rows, append([]any{rt, id, code, ord}, vals...))
	}
	// addIndex ghi các mục của một chỉ mục; ordOf cho ord của mục thứ i (mặc định là chính i; composite dùng chỉ số phần tử)
	addIndex := func(code string, ix *fhirsearch.Index, ordOf func(i int) int) {
		for i, e := range ix.Strings {
			add("string", code, ordOf(i), e.Raw, e.Norm)
		}
		for i, e := range ix.Tokens {
			var sys any
			if e.HasSystem {
				sys = e.System
			}
			add("token", code, ordOf(i), "v", sys, e.Code)
		}
		for i, t := range ix.Texts {
			add("token", code, ordOf(i), "t", nil, t)
		}
		for i, e := range ix.References {
			add("reference", code, ordOf(i), e.Raw, e.Target, e.Type, e.ID)
		}
		for i, e := range ix.Dates {
			add("date", code, ordOf(i), e.Lo, e.Hi)
		}
		for i, n := range ix.Numbers {
			add("number", code, ordOf(i), n)
		}
		for i, e := range ix.Quantities {
			add("quantity", code, ordOf(i), e.Value, e.System, e.Code, e.Unit)
		}
		for i, u := range ix.URIs {
			add("uri", code, ordOf(i), u.Raw, u.Norm)
		}
	}
	seq := func(i int) int { return i }
	for _, def := range indexedParams(rt) {
		ix, err := fhirsearch.Extract(doc, def)
		if err != nil {
			return internal(err)
		}
		if !ix.Empty() {
			addIndex(def.Code, ix, seq)
		}
	}
	for _, def := range compositeParams(rt) {
		entries, err := fhirsearch.ExtractComposite(doc, def)
		if err != nil {
			return internal(err)
		}
		for _, e := range entries {
			ord := e.Ord
			e.Ix.Texts = nil // :text không dùng cho thành phần composite
			addIndex(e.Code, e.Ix, func(int) int { return ord })
		}
	}
	for _, t := range indexTables {
		if err := s.insertRows(ctx, q, "fhir_idx_"+t, sets[t]); err != nil {
			return err
		}
	}
	return nil
}

// insertRows ghi nhiều dòng bằng các câu INSERT nhiều VALUES (chia lô để không vượt giới hạn tham số).
func (s *Store) insertRows(ctx context.Context, q querier, table string, rs *rowSet) error {
	if len(rs.rows) == 0 {
		return nil
	}
	cols := append([]string{"resource_type", "id", "code", "ord"}, rs.cols...)
	const maxParams = 30000
	per := max(1, maxParams/len(cols))
	for start := 0; start < len(rs.rows); start += per {
		end := min(start+per, len(rs.rows))
		b := &qb{d: s.d}
		b.w("INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES ")
		for i, row := range rs.rows[start:end] {
			if i > 0 {
				b.w(", ")
			}
			b.w("(")
			for j, v := range row {
				if j > 0 {
					b.w(", ")
				}
				b.w(b.arg(v))
			}
			b.w(")")
		}
		if _, err := q.ExecContext(ctx, b.sb.String(), b.args...); err != nil {
			return internal(err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- quản trị

const reindexBatch = 200

// Reindex lập lại chỉ mục search của mọi resource chưa xóa thuộc kiểu rt (hoặc của đúng một resource nếu id khác rỗng),
// theo lô, mỗi lô một transaction. progress (có thể nil) được gọi sau mỗi lô với số resource đã xử lý.
func (s *Store) Reindex(ctx context.Context, rt, id string, progress func(done int)) (int, error) {
	done, cursor := 0, ""
	for {
		var rows []struct {
			id      string
			content []byte
		}
		err := s.inTx(ctx, func(q querier) error {
			b := &qb{d: s.d}
			query := "SELECT id, content FROM fhir_resource WHERE resource_type = " + b.arg(rt) + " AND deleted = " + b.arg(false) + " AND id > " + b.arg(cursor)
			if id != "" {
				query += " AND id = " + b.arg(id)
			}
			query += " ORDER BY id" + s.d.collateC() + " LIMIT " + b.arg(reindexBatch)
			rs, err := q.QueryContext(ctx, query, b.args...)
			if err != nil {
				return internal(err)
			}
			for rs.Next() {
				var r struct {
					id      string
					content []byte
				}
				if err := rs.Scan(&r.id, &r.content); err != nil {
					rs.Close()
					return internal(err)
				}
				rows = append(rows, r)
			}
			if err := rs.Err(); err != nil {
				rs.Close()
				return internal(err)
			}
			rs.Close()
			for _, r := range rows {
				doc, err := decodeDoc(r.content)
				if err != nil {
					return internal(err)
				}
				if err := s.reindex(ctx, q, rt, r.id, doc); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return done, err
		}
		done += len(rows)
		if progress != nil {
			progress(done)
		}
		if len(rows) < reindexBatch {
			return done, nil
		}
		cursor = rows[len(rows)-1].id
	}
}

// Expunge xóa vĩnh viễn resource cùng toàn bộ lịch sử và chỉ mục (không hoàn tác được).
func (s *Store) Expunge(ctx context.Context, rt, id string) error {
	return s.inTx(ctx, func(q querier) error {
		b := &qb{d: s.d}
		var n int
		if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM fhir_history WHERE resource_type = "+b.arg(rt)+" AND id = "+b.arg(id), b.args...).Scan(&n); err != nil {
			return internal(err)
		}
		if n == 0 {
			return fhirrouter.NotFound()
		}
		if err := s.deleteIndex(ctx, q, rt, id); err != nil {
			return err
		}
		for _, t := range []string{"fhir_history", "fhir_resource"} {
			b := &qb{d: s.d}
			if _, err := q.ExecContext(ctx, "DELETE FROM "+t+" WHERE resource_type = "+b.arg(rt)+" AND id = "+b.arg(id), b.args...); err != nil {
				return internal(err)
			}
		}
		return nil
	})
}

var (
	_ fhirrouter.Reindexer = (*Store)(nil)
	_ fhirrouter.Expunger  = (*Store)(nil)
)
