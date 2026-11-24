package fhirstore

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

func (s *Store) Search(ctx context.Context, req *fhirsearch.Request) (*fhirrouter.SearchResult, error) {
	if fhirsearch.NeedsResolution(req) {
		return nil, fhirrouter.NotImplemented("chained search, _has và modifier token :in/:not-in/:above/:below phải được giải trước khi gọi Repository (dùng Router)")
	}
	types := req.Types
	if req.ResourceType != "" {
		types = []string{req.ResourceType}
	}
	b := &qb{d: s.d}
	where, err := s.whereClause(b, req, types)
	if err != nil {
		return nil, err
	}
	res := &fhirrouter.SearchResult{}
	whereArgs := len(b.args)

	if req.Total != "none" {
		var n int
		if err := s.q().QueryRowContext(ctx, "SELECT COUNT(*) FROM fhir_resource r WHERE "+where, b.args[:whereArgs]...).Scan(&n); err != nil {
			return nil, internal(err)
		}
		res.Total = &n
	}
	if req.Count == 0 {
		return res, nil
	}

	order, err := s.orderClause(b, req, types)
	if err != nil {
		return nil, err
	}
	query := "SELECT r.content FROM fhir_resource r WHERE " + where + " ORDER BY " + order +
		" LIMIT " + b.arg(req.Count+1) + " OFFSET " + b.arg(req.Offset)
	rows, err := s.q().QueryContext(ctx, query, b.args...)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	var page [][]byte
	for rows.Next() {
		var c []byte
		if err := rows.Scan(&c); err != nil {
			return nil, internal(err)
		}
		page = append(page, c)
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	if len(page) > req.Count {
		res.More = true
		page = page[:req.Count]
	}
	for _, c := range page {
		r, err := resourceOf(c)
		if err != nil {
			return nil, err
		}
		res.Matches = append(res.Matches, r)
	}
	if res.Included, err = s.includes(ctx, page, req); err != nil {
		return nil, err
	}
	return res, nil
}

// ---------------------------------------------------------------- WHERE

func (s *Store) whereClause(b *qb, req *fhirsearch.Request, types []string) (string, error) {
	parts := []string{"r.deleted = " + b.arg(false)}
	if len(types) > 0 {
		parts = append(parts, "r.resource_type IN ("+s.placeholders(b, types)+")")
	}
	for _, f := range req.Filters {
		sqlf, err := s.filterSQL(b, f, req, types)
		if err != nil {
			return "", err
		}
		parts = append(parts, sqlf)
	}
	for _, e := range req.Exprs {
		sqle, err := s.exprSQL(b, e, req, types)
		if err != nil {
			return "", err
		}
		parts = append(parts, sqle)
	}
	return strings.Join(parts, " AND "), nil
}

// exprSQL dịch biểu thức _filter: lá là một điều kiện thường, còn lại ghép bằng AND/OR/NOT. Placeholder được cấp theo
// đúng thứ tự văn bản (duyệt trái sang phải).
func (s *Store) exprSQL(b *qb, e *fhirsearch.Expr, req *fhirsearch.Request, types []string) (string, error) {
	switch e.Op {
	case "leaf":
		return s.filterSQL(b, *e.Leaf, req, types)
	case "not":
		inner, err := s.exprSQL(b, e.Kids[0], req, types)
		return "NOT (" + inner + ")", err
	case "and", "or":
		parts := make([]string, len(e.Kids))
		for i, k := range e.Kids {
			var err error
			if parts[i], err = s.exprSQL(b, k, req, types); err != nil {
				return "", err
			}
		}
		return "(" + strings.Join(parts, " "+strings.ToUpper(e.Op)+" ") + ")", nil
	}
	return "", &fhirsearch.Error{Msg: "nút _filter không hợp lệ"}
}

func (s *Store) placeholders(b *qb, vals []string) string {
	ph := make([]string, len(vals))
	for i, v := range vals {
		ph[i] = b.arg(v)
	}
	return strings.Join(ph, ", ")
}

func idxTable(def fhir.SearchParamDef) (string, error) {
	switch def.Type {
	case "string", "token", "reference", "date", "number", "quantity", "uri":
		return "fhir_idx_" + def.Type, nil
	}
	return "", &fhirsearch.Error{Msg: "kiểu tham số " + def.Type + " chưa được hỗ trợ"}
}

func (s *Store) filterSQL(b *qb, f fhirsearch.Filter, req *fhirsearch.Request, types []string) (string, error) {
	if req.ResourceType == "" {
		// search hệ thống: mỗi kiểu có định nghĩa riêng cho cùng một code, và kiểu không khai báo tham số thì loại
		var alts []string
		for _, t := range types {
			def, found := fhirsearch.Lookup(t, f.Code)
			if !found {
				continue
			}
			ft := f
			ft.Def = def
			typePh := b.arg(t) // trước filterOne: placeholder ? khớp theo thứ tự văn bản
			one, err := s.filterOne(b, ft, t)
			if err != nil {
				return "", err
			}
			alts = append(alts, "(r.resource_type = "+typePh+" AND "+one+")")
		}
		if len(alts) == 0 {
			return "1 = 0", nil
		}
		return "(" + strings.Join(alts, " OR ") + ")", nil
	}
	return s.filterOne(b, f, req.ResourceType)
}

// ownerMatch dựng điều kiện nối bảng chỉ mục x với resource r. Khi đã biết loại resource (typ), dùng dạng `r.id IN (SELECT x.id ... WHERE x.resource_type = <typ> ...)`
// thay cho EXISTS tương quan: SQLite không đảo được EXISTS tương quan để đi từ chỉ mục tìm kiếm, nên mỗi tìm kiếm quét cả loại (đo: 16,7 ms mỗi truy vấn với 20 000
// instance, dạng IN chỉ 0,01 ms, nhanh hơn khoảng 1700 lần; PostgreSQL đổi hai dạng thành cùng một semi-join). Hai dạng tương đương vì r.resource_type = typ đã được
// ràng buộc ở mệnh đề ngoài. typ rỗng (tìm kiếm trên nhiều loại) giữ dạng EXISTS.
func ownerMatch(b *qb, typ, table string, negate bool, rest func() string) string {
	if typ == "" {
		owner := "x.resource_type = r.resource_type AND x.id = r.id AND x.code = "
		sql := "EXISTS (SELECT 1 FROM " + table + " x WHERE " + owner + rest() + ")"
		if negate {
			sql = "NOT " + sql
		}
		return sql
	}
	typePh := b.arg(typ)
	op := "IN"
	if negate {
		op = "NOT IN"
	}
	return "r.id " + op + " (SELECT x.id FROM " + table + " x WHERE x.resource_type = " + typePh + " AND x.code = " + rest() + ")"
}

// filterOne dịch một Filter (đã có Def đúng) thành biểu thức trên fhir_resource r. typ là loại resource của r nếu biết (xem ownerMatch).
func (s *Store) filterOne(b *qb, f fhirsearch.Filter, typ string) (string, error) {
	if f.Def.Type == "composite" {
		return s.compositeSQL(b, f)
	}
	var restrict string
	if f.Code == "_id" {
		return restrict + s.idFilter(b, f), nil
	}
	table, err := idxTable(f.Def)
	if err != nil {
		return "", err
	}
	if f.Modifier == "missing" {
		return restrict + ownerMatch(b, typ, table, f.Values[0] == "true", func() string { return b.arg(f.Code) }), nil
	}
	conds, err := fhirsearch.Conds(f)
	if err != nil {
		return "", err
	}
	// placeholder ? phải được cấp theo đúng thứ tự văn bản: loại và code trước, rồi các điều kiện
	return restrict + ownerMatch(b, typ, table, f.Modifier == "not", func() string {
		code := b.arg(f.Code)
		var ors []string
		if f.Def.Type == "reference" && len(conds) > 1 {
			ors = s.referenceORs(b, conds) // nhiều giá trị (vd kết quả chained search): gom thành IN thay vì chuỗi OR dài
		} else {
			ors = make([]string, len(conds))
			for i, c := range conds {
				ors[i] = s.condSQL(b, f, c)
			}
		}
		return code + " AND (" + strings.Join(ors, " OR ") + ")"
	}), nil
}

// referenceORs gom nhiều điều kiện tham chiếu (OR với nhau) thành vài vế IN: `x.target IN (...)`, `x.raw IN (...)` và, cho giá trị
// chỉ có id, `x.ref_id IN (...)` theo từng tập kiểu đích. Tương đương logic với từng điều kiện riêng của condSQL nhưng để trình
// tối ưu dùng được chỉ mục (loại, code, target) thay vì thử hàng trăm phép so sánh trên mỗi dòng. Placeholder cấp theo đúng thứ tự
// văn bản của các vế trả về.
func (s *Store) referenceORs(b *qb, conds []fhirsearch.Cond) []string {
	seen := map[string]bool{}
	uniq := func(prefix, v string) bool {
		k := prefix + "\x00" + v
		if seen[k] {
			return false
		}
		seen[k] = true
		return true
	}
	var targets, raws []string
	type idGroup struct {
		anyTarget bool
		types     []string
		ids       []string
	}
	groups := map[string]*idGroup{}
	var order []string
	for _, c := range conds {
		if uniq("t", c.Ref) {
			targets = append(targets, c.Ref)
		}
		if uniq("r", c.Raw) {
			raws = append(raws, c.Raw)
		}
		if c.IDOnly != "" {
			key := strconv.FormatBool(c.AnyTarget) + "|" + strings.Join(c.Targets, ",")
			g := groups[key]
			if g == nil {
				g = &idGroup{anyTarget: c.AnyTarget, types: c.Targets}
				groups[key] = g
				order = append(order, key)
			}
			if uniq("i"+key, c.IDOnly) {
				g.ids = append(g.ids, c.IDOnly)
			}
		}
	}
	var ors []string
	if len(targets) > 0 {
		ors = append(ors, "x.target IN ("+s.placeholders(b, targets)+")")
	}
	if len(raws) > 0 {
		ors = append(ors, "x.raw IN ("+s.placeholders(b, raws)+")")
	}
	for _, key := range order {
		g := groups[key]
		sql := "(x.ref_id IN (" + s.placeholders(b, g.ids) + ")"
		if !g.anyTarget {
			sql += " AND x.ref_type IN (" + s.placeholders(b, g.types) + ")"
		}
		ors = append(ors, sql+")")
	}
	return ors
}

// compositeSQL dịch tham số composite: mỗi giá trị OR là một chuỗi EXISTS lồng nhau, thành phần sau nối với thành phần
// trước theo cùng (resource, ord) nên mọi thành phần phải khớp trên cùng một phần tử. Thành phần thứ i dùng bí danh c<i>.
func (s *Store) compositeSQL(b *qb, f fhirsearch.Filter) (string, error) {
	vals, err := fhirsearch.CompositeConds(f)
	if err != nil {
		return "", err
	}
	ors := make([]string, len(vals))
	for j, parts := range vals {
		var chain func(i int) (string, error)
		chain = func(i int) (string, error) {
			p := parts[i]
			table, err := idxTable(p.Def)
			if err != nil {
				return "", err
			}
			alias := "c" + strconv.Itoa(i)
			join := alias + ".resource_type = r.resource_type AND " + alias + ".id = r.id"
			if i > 0 {
				prev := "c" + strconv.Itoa(i-1)
				join = alias + ".resource_type = " + prev + ".resource_type AND " + alias + ".id = " + prev + ".id AND " + alias + ".ord = " + prev + ".ord"
			}
			codePh := b.arg(p.Code) // cấp placeholder theo đúng thứ tự văn bản
			cond := strings.ReplaceAll(s.condSQL(b, fhirsearch.Filter{Code: p.Def.Code, Def: p.Def}, p.Cond), "x.", alias+".")
			sql := "EXISTS (SELECT 1 FROM " + table + " " + alias + " WHERE " + join + " AND " + alias + ".code = " + codePh + " AND (" + cond + ")"
			if i+1 < len(parts) {
				next, err := chain(i + 1)
				if err != nil {
					return "", err
				}
				sql += " AND " + next
			}
			return sql + ")", nil
		}
		if ors[j], err = chain(0); err != nil {
			return "", err
		}
	}
	return "(" + strings.Join(ors, " OR ") + ")", nil
}

func (s *Store) idFilter(b *qb, f fhirsearch.Filter) string {
	if f.Modifier == "missing" {
		if f.Values[0] == "true" {
			return "1 = 0"
		}
		return "1 = 1"
	}
	ors := make([]string, len(f.Values))
	for i, v := range f.Values {
		ors[i] = "r.id = " + b.arg(fhirsearch.Unescape(v))
	}
	cond := "(" + strings.Join(ors, " OR ") + ")"
	if f.Modifier == "not" {
		return "NOT " + cond
	}
	return cond
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// condSQL dịch một điều kiện của một giá trị OR thành biểu thức trên bảng chỉ mục (bí danh x).
func (s *Store) condSQL(b *qb, f fhirsearch.Filter, c fhirsearch.Cond) string {
	switch c.Kind {
	case "string":
		switch c.Mode {
		case "exact":
			return "x.raw = " + b.arg(c.Raw)
		case "contains":
			return s.d.contains("x.norm", b.arg(c.Norm))
		}
		return "substr(x.norm, 1, " + strconv.Itoa(c.Len) + ") = " + b.arg(c.Norm)
	case "token":
		switch {
		case c.Text:
			return "(x.kind = 't' AND substr(x.value, 1, " + strconv.Itoa(c.Len) + ") = " + b.arg(c.Norm) + ")"
		case !c.HasSystem:
			return "(x.kind = 'v' AND x.value = " + b.arg(c.Code) + ")"
		case c.System == "":
			return "(x.kind = 'v' AND x.system IS NULL AND x.value = " + b.arg(c.Code) + ")"
		case c.Code == "":
			return "(x.kind = 'v' AND x.system = " + b.arg(c.System) + ")"
		}
		return "(x.kind = 'v' AND x.system = " + b.arg(c.System) + " AND x.value = " + b.arg(c.Code) + ")"
	case "reference":
		sql := "(x.target = " + b.arg(c.Ref) + " OR x.raw = " + b.arg(c.Raw)
		if c.IDOnly != "" {
			sql += " OR (x.ref_id = " + b.arg(c.IDOnly)
			if !c.AnyTarget {
				sql += " AND x.ref_type IN (" + s.placeholders(b, c.Targets) + ")"
			}
			sql += ")"
		}
		return sql + ")"
	case "date":
		return s.dateSQL(b, c)
	case "number":
		return s.numberSQL(b, "x.value", c)
	case "quantity":
		sql := s.numberSQL(b, "x.value", c)
		if c.QCode != "" {
			sql += " AND (x.qcode = " + b.arg(c.QCode) + " OR x.unit = " + b.arg(c.QCode) + ")"
			if c.QSys != "" {
				sql += " AND x.system = " + b.arg(c.QSys)
			}
		}
		return "(" + sql + ")"
	case "uri":
		switch c.Mode {
		case "below":
			return "substr(x.value, 1, " + strconv.Itoa(runes(c.Raw)) + ") = " + b.arg(c.Raw)
		case "above":
			return "substr(" + s.d.text(b.arg(c.Raw)) + ", 1, length(x.value)) = x.value"
		}
		return "x.value = " + b.arg(c.Raw)
	}
	return "1 = 0"
}

func (s *Store) dateSQL(b *qb, c fhirsearch.Cond) string {
	within := func() string {
		return "(x.lo >= " + b.arg(c.Lo) + " AND x.hi <= " + b.arg(c.Hi) + ")"
	}
	switch c.Prefix {
	case "eq":
		return within()
	case "ne":
		return "NOT " + within()
	case "gt":
		return "x.hi > " + b.arg(c.Hi)
	case "lt":
		return "x.lo < " + b.arg(c.Lo)
	case "ge":
		return "(x.hi > " + b.arg(c.Hi) + " OR " + within() + ")"
	case "le":
		return "(x.lo < " + b.arg(c.Lo) + " OR " + within() + ")"
	case "sa":
		return "x.lo >= " + b.arg(c.Hi)
	case "eb":
		return "x.hi <= " + b.arg(c.Lo)
	case "ap":
		return "(x.lo < " + b.arg(c.Hi+c.Margin) + " AND x.hi > " + b.arg(c.Lo-c.Margin) + ")"
	}
	return "1 = 0"
}

func (s *Store) numberSQL(b *qb, col string, c fhirsearch.Cond) string {
	switch c.Prefix {
	case "eq":
		return "(" + col + " >= " + b.arg(c.NLo) + " AND " + col + " < " + b.arg(c.NHi) + ")"
	case "ne":
		return "NOT (" + col + " >= " + b.arg(c.NLo) + " AND " + col + " < " + b.arg(c.NHi) + ")"
	case "gt", "sa":
		return col + " > " + b.arg(c.V)
	case "lt", "eb":
		return col + " < " + b.arg(c.V)
	case "ge":
		return col + " >= " + b.arg(c.V)
	case "le":
		return col + " <= " + b.arg(c.V)
	case "ap":
		return "ABS(" + col + " - " + b.arg(c.V) + ") <= " + b.arg(fhirsearch.AbsNumberMargin(c.V))
	}
	return "1 = 0"
}

// ---------------------------------------------------------------- ORDER BY

func (s *Store) orderClause(b *qb, req *fhirsearch.Request, types []string) (string, error) {
	var parts []string
	for _, r := range req.Sort {
		dir := "ASC"
		if r.Descending {
			dir = "DESC"
		}
		// thiếu giá trị luôn xếp cuối, bất kể chiều sắp xếp. Khóa được dựng hai lần (mỗi lần xuất hiện
		// trong câu SQL có tham số riêng) để placeholder ? khớp thứ tự văn bản.
		k1, err := s.sortKey(b, r)
		if err != nil {
			return "", err
		}
		parts = append(parts, "CASE WHEN ("+k1+") IS NULL THEN 1 ELSE 0 END ASC")
		k2, err := s.sortKey(b, r)
		if err != nil {
			return "", err
		}
		parts = append(parts, "("+k2+") "+dir)
	}
	// khóa phụ: thứ tự kiểu (theo _type nếu có) rồi id, khớp MemoryRepository
	if len(types) > 1 {
		var cases []string
		for i, t := range types {
			cases = append(cases, "WHEN "+b.arg(t)+" THEN "+strconv.Itoa(i))
		}
		parts = append(parts, "CASE r.resource_type "+strings.Join(cases, " ")+" END")
	} else if len(types) == 0 {
		parts = append(parts, "r.resource_type"+s.d.collateC())
	}
	parts = append(parts, "r.id"+s.d.collateC())
	return strings.Join(parts, ", "), nil
}

// sortKey trả về biểu thức khóa sắp xếp: giá trị đầu tiên (theo thứ tự trích) của tham số.
func (s *Store) sortKey(b *qb, r fhirsearch.SortRule) (string, error) {
	if r.Code == "_id" {
		return "r.id" + s.d.collateC(), nil
	}
	table, err := idxTable(r.Def)
	if err != nil {
		return "", err
	}
	var col, extra string
	switch r.Def.Type {
	case "string":
		col = "x.norm" + s.d.collateC()
	case "token":
		col, extra = "x.value"+s.d.collateC(), " AND x.kind = 'v'"
	case "reference":
		col = "x.raw" + s.d.collateC()
	case "date":
		col = "x.lo"
	case "number", "quantity":
		col = "x.value"
	case "uri":
		col = "x.norm" + s.d.collateC()
	default:
		return "", &fhirsearch.Error{Msg: "chưa hỗ trợ sắp xếp theo tham số kiểu " + r.Def.Type}
	}
	return "SELECT " + col + " FROM " + table + " x WHERE x.resource_type = r.resource_type AND x.id = r.id AND x.code = " +
		b.arg(r.Code) + extra + " ORDER BY x.ord LIMIT 1", nil
}

// ---------------------------------------------------------------- include

func (s *Store) includes(ctx context.Context, page [][]byte, req *fhirsearch.Request) ([]fhir.Resource, error) {
	if len(req.Include) == 0 && len(req.Revinclude) == 0 {
		return nil, nil
	}
	docs := make([]map[string]any, len(page))
	seen := map[string]bool{}
	var keys []string
	for i, c := range page {
		d, err := decodeDoc(c)
		if err != nil {
			return nil, internal(err)
		}
		docs[i] = d
		k := d["resourceType"].(string) + "/" + d["id"].(string)
		seen[k] = true
		keys = append(keys, k)
	}
	var out []fhir.Resource
	emit := func(content []byte, key string) error {
		if seen[key] {
			return nil
		}
		seen[key] = true
		r, err := resourceOf(content)
		if err != nil {
			return err
		}
		out = append(out, r)
		return nil
	}
	// _include: gom tham chiếu theo thứ tự gặp rồi lấy một lượt
	var wanted []string
	for _, inc := range req.Include {
		for _, d := range docs {
			rt := d["resourceType"].(string)
			if inc.ResourceType != "*" && inc.ResourceType != rt {
				continue
			}
			def, ok := fhirsearch.Lookup(rt, inc.Param)
			if !ok {
				return nil, &fhirsearch.Error{Msg: "_include: tham số không tồn tại: " + inc.Param}
			}
			refs, err := fhirsearch.References(d, def)
			if err != nil {
				return nil, err
			}
			for _, ref := range refs {
				i := strings.LastIndex(ref, "/")
				if i <= 0 || !fhir.IsResourceType(ref[:i]) || (inc.Target != "" && inc.Target != ref[:i]) {
					continue
				}
				wanted = append(wanted, ref)
			}
		}
	}
	if len(wanted) > 0 {
		fetched, err := s.fetchByKeys(ctx, wanted)
		if err != nil {
			return nil, err
		}
		for _, k := range wanted {
			if c, ok := fetched[k]; ok {
				if err := emit(c, k); err != nil {
					return nil, err
				}
			}
		}
	}
	// _revinclude: resource của kiểu rev.ResourceType có tham chiếu tới kết quả
	for _, rev := range req.Revinclude {
		def, ok := fhirsearch.Lookup(rev.ResourceType, rev.Param)
		if !ok {
			return nil, &fhirsearch.Error{Msg: "_revinclude: tham số không tồn tại: " + rev.Param}
		}
		if _, err := idxTable(def); err != nil || def.Type != "reference" {
			continue
		}
		b := &qb{d: s.d}
		q := "SELECT r.content, r.id FROM fhir_resource r WHERE r.deleted = " + b.arg(false) + " AND r.resource_type = " + b.arg(rev.ResourceType) +
			" AND EXISTS (SELECT 1 FROM fhir_idx_reference x WHERE x.resource_type = r.resource_type AND x.id = r.id AND x.code = " + b.arg(def.Code) +
			" AND x.target IN (" + s.placeholders(b, keys) + ")) ORDER BY r.id" + s.d.collateC()
		rows, err := s.q().QueryContext(ctx, q, b.args...)
		if err != nil {
			return nil, internal(err)
		}
		type item struct {
			c  []byte
			id string
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.c, &it.id); err != nil {
				rows.Close()
				return nil, internal(err)
			}
			items = append(items, it)
		}
		rows.Close()
		for _, it := range items {
			if err := emit(it.c, rev.ResourceType+"/"+it.id); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// fetchByKeys lấy nội dung các resource chưa xóa theo danh sách "Type/id".
func (s *Store) fetchByKeys(ctx context.Context, keys []string) (map[string][]byte, error) {
	uniq := slices.Compact(slices.Sorted(slices.Values(keys)))
	out := map[string][]byte{}
	const chunk = 500
	for start := 0; start < len(uniq); start += chunk {
		part := uniq[start:min(start+chunk, len(uniq))]
		b := &qb{d: s.d}
		notDeleted := b.arg(false) // trước các điều kiện, đúng thứ tự văn bản
		var conds []string
		for _, k := range part {
			i := strings.LastIndex(k, "/")
			conds = append(conds, "(resource_type = "+b.arg(k[:i])+" AND id = "+b.arg(k[i+1:])+")")
		}
		rows, err := s.q().QueryContext(ctx, "SELECT resource_type, id, content FROM fhir_resource WHERE deleted = "+notDeleted+" AND ("+strings.Join(conds, " OR ")+")", b.args...)
		if err != nil {
			return nil, internal(err)
		}
		for rows.Next() {
			var rt, id string
			var c []byte
			if err := rows.Scan(&rt, &id, &c); err != nil {
				rows.Close()
				return nil, internal(err)
			}
			out[rt+"/"+id] = c
		}
		rows.Close()
	}
	return out, nil
}
