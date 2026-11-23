package fhirauth

import (
	"bytes"
	"context"
	"encoding/json"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// Repo bọc một Repository để người gọi chỉ thấy và chỉ ghi resource thuộc project của mình (Meta.project).
//
//   - Thiếu Principal (hoặc Principal không có project) trong context: từ chối (403), không bao giờ mở.
//   - Tạo/sửa: Meta.project luôn bị ghi đè bằng project của người gọi (giá trị client gửi bị bỏ).
//   - Đọc, đọc phiên bản, lịch sử, sửa, xóa, expunge: resource của project khác trả 404 như thể không tồn tại.
//   - Search: thêm điều kiện _project; kết quả _include/_revinclude bị lọc theo project.
//
// Giới hạn: resource đã xóa không còn nội dung để kiểm project nên đọc id đã xóa của project khác trả 410 thay vì 404
// (id là UUID ngẫu nhiên nên không đoán được). Reindex cả loại (id rỗng) bị từ chối vì chạy xuyên project;
// job $reindex chạy bằng Options.JobRepository không tách.
// Dữ liệu ghi trước khi bật tách project không có Meta.project nên sẽ không ai thấy cho tới khi được gán project.
type Repo struct{ inner fhirrouter.Repository }

// Scope bọc repository.
func Scope(inner fhirrouter.Repository) *Repo { return &Repo{inner: inner} }

var (
	_ fhirrouter.Repository    = (*Repo)(nil)
	_ fhirrouter.Transactional = (*Repo)(nil)
	_ fhirrouter.Reindexer     = (*Repo)(nil)
	_ fhirrouter.Expunger      = (*Repo)(nil)
)

func projectOf(ctx context.Context) (string, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.Project == "" {
		return "", fhirrouter.Forbidden("Không xác định được project của người gọi")
	}
	return p.Project, nil
}

// allow kiểm quyền cho MỘT thao tác (need là một bit Perm) trên loại resource, theo Principal.Grants và chính sách truy cập.
// Từ chối bằng 403 kèm chữ SMART của quyền còn thiếu.
func allow(ctx context.Context, rt string, need Perm) error {
	if rt == jobType { // trạng thái job nền: không giới hạn theo loại (đã tách project, và job chạy chỉ khi chữ ký quyền hợp lệ)
		return nil
	}
	p, _ := PrincipalFrom(ctx)
	if p == nil {
		return fhirrouter.Forbidden("Không xác định được người gọi")
	}
	if !p.Policy.allows(rt, need&PermWrite != 0) {
		return fhirrouter.Forbidden("Chính sách truy cập không cho phép " + rt)
	}
	if !p.Grants.Can(rt, need) {
		return fhirrouter.Forbidden("Token thiếu quyền " + describe(need) + " (SMART) trên " + rt)
	}
	return nil
}

// CanUpdate cho biết người gọi trong ctx được cập nhật resource loại rt: vai trò từ write trở lên, chính sách không chỉ-đọc và scope SMART
// có quyền u. Thiếu Principal thì false. Dùng để quyết định cấp quyền ghi gián tiếp (URL tải lên của Binary).
func CanUpdate(ctx context.Context, rt string) bool {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.Role < RoleWrite {
		return false
	}
	return allow(ctx, rt, PermUpdate) == nil
}

// jobType được miễn kiểm theo loại để router tạo, đọc, hủy job của chính người gọi.
const jobType = "AsyncJob"

func grantsSuper(ctx context.Context) bool {
	p, _ := PrincipalFrom(ctx)
	return p != nil && p.Super
}

func policyOf(ctx context.Context) *Policy {
	p, _ := PrincipalFrom(ctx)
	if p == nil {
		return nil
	}
	return p.Policy
}

func grantsOf(ctx context.Context) Grants {
	p, _ := PrincipalFrom(ctx)
	if p == nil {
		return nil
	}
	return p.Grants
}

func inProject(res fhir.Resource, project string) bool {
	if res == nil {
		return false
	}
	m := res.GetMeta()
	return m != nil && m.Project == project
}

// stamp trả bản sao của res với Meta.project = project (qua JSON vì resource có thể chưa có Meta).
func stamp(res fhir.Resource, project string) (fhir.Resource, error) {
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, fhirrouter.Internal(err.Error())
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var doc map[string]any
	if err := d.Decode(&doc); err != nil {
		return nil, fhirrouter.Internal(err.Error())
	}
	meta, _ := doc["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["project"] = project
	doc["meta"] = meta
	raw, err = json.Marshal(doc)
	if err != nil {
		return nil, fhirrouter.Internal(err.Error())
	}
	out, err := fhir.UnmarshalResource(raw)
	if err != nil {
		return nil, fhirrouter.Internal(err.Error())
	}
	return out, nil
}

// owned kiểm tra resource hiện tại (nếu có) thuộc project. Trả lỗi gốc nếu chưa có hoặc đã xóa.
func (r *Repo) owned(ctx context.Context, project, rt, id string) error {
	cur, err := r.inner.ReadResource(ctx, rt, id)
	if err != nil {
		return err
	}
	if !inProject(cur, project) || !policyOf(ctx).matches(cur) {
		return fhirrouter.NotFound()
	}
	return nil
}

// ownedByHistory dùng cho resource có thể đã xóa: project lấy từ phiên bản còn nội dung bất kỳ.
func (r *Repo) ownedByHistory(ctx context.Context, project, rt, id string) error {
	h, err := r.inner.ReadHistory(ctx, rt, id)
	if err != nil {
		return err
	}
	for _, e := range h {
		if !e.Deleted && e.Resource != nil {
			if inProject(e.Resource, project) && policyOf(ctx).matches(e.Resource) {
				return nil
			}
			return fhirrouter.NotFound()
		}
	}
	return fhirrouter.NotFound()
}

func (r *Repo) CreateResource(ctx context.Context, res fhir.Resource) (fhir.Resource, error) {
	project, err := projectOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := allow(ctx, res.GetResourceType(), PermCreate); err != nil {
		return nil, err
	}
	s, err := stamp(res, project)
	if err != nil {
		return nil, err
	}
	if !policyOf(ctx).matches(s) {
		return nil, fhirrouter.Forbidden("Resource không thỏa tiêu chí của chính sách truy cập")
	}
	return r.inner.CreateResource(ctx, s)
}

func (r *Repo) ReadResource(ctx context.Context, rt, id string) (fhir.Resource, error) {
	project, err := projectOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := allow(ctx, rt, PermRead); err != nil {
		return nil, err
	}
	res, err := r.inner.ReadResource(ctx, rt, id)
	if err != nil {
		return nil, err
	}
	if !inProject(res, project) || !policyOf(ctx).matches(res) {
		return nil, fhirrouter.NotFound()
	}
	return res, nil
}

func (r *Repo) ReadVersion(ctx context.Context, rt, id, vid string) (fhir.Resource, error) {
	project, err := projectOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := allow(ctx, rt, PermRead); err != nil {
		return nil, err
	}
	res, err := r.inner.ReadVersion(ctx, rt, id, vid)
	if err != nil {
		return nil, err
	}
	if !inProject(res, project) || !policyOf(ctx).matches(res) {
		return nil, fhirrouter.NotFound()
	}
	return res, nil
}

func (r *Repo) ReadHistory(ctx context.Context, rt, id string) ([]fhirrouter.HistoryEntry, error) {
	project, err := projectOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := allow(ctx, rt, PermRead); err != nil {
		return nil, err
	}
	h, err := r.inner.ReadHistory(ctx, rt, id)
	if err != nil {
		return nil, err
	}
	owned := false
	for _, e := range h {
		if !e.Deleted && e.Resource != nil {
			if !inProject(e.Resource, project) || !policyOf(ctx).matches(e.Resource) {
				return nil, fhirrouter.NotFound()
			}
			owned = true
		}
	}
	if !owned {
		return nil, fhirrouter.NotFound()
	}
	return h, nil
}

func (r *Repo) UpdateResource(ctx context.Context, res fhir.Resource, o fhirrouter.UpdateOptions) (fhir.Resource, error) {
	project, err := projectOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := allow(ctx, res.GetResourceType(), PermUpdate); err != nil {
		return nil, err
	}
	if err := r.owned(ctx, project, res.GetResourceType(), res.GetID()); err != nil {
		return nil, err
	}
	s, err := stamp(res, project)
	if err != nil {
		return nil, err
	}
	if !policyOf(ctx).matches(s) {
		return nil, fhirrouter.Forbidden("Resource không thỏa tiêu chí của chính sách truy cập")
	}
	return r.inner.UpdateResource(ctx, s, o)
}

func (r *Repo) DeleteResource(ctx context.Context, rt, id string) error {
	project, err := projectOf(ctx)
	if err != nil {
		return err
	}
	if err := allow(ctx, rt, PermDelete); err != nil {
		return err
	}
	if err := r.owned(ctx, project, rt, id); err != nil {
		return err
	}
	return r.inner.DeleteResource(ctx, rt, id)
}

func (r *Repo) Search(ctx context.Context, req *fhirsearch.Request) (*fhirrouter.SearchResult, error) {
	project, err := projectOf(ctx)
	if err != nil {
		return nil, err
	}
	g := grantsOf(ctx)
	scoped := *req
	switch {
	case req.ResourceType != "":
		if err := allow(ctx, req.ResourceType, PermSearch); err != nil {
			return nil, err
		}
	case len(req.Types) > 0:
		for _, t := range req.Types {
			if err := allow(ctx, t, PermSearch); err != nil {
				return nil, err
			}
		}
	case !g.FullSearch():
		scoped.Types = g.Searchable() // search hệ thống: chỉ các loại được tìm kiếm
	}
	pol := policyOf(ctx)
	if pol != nil {
		if req.ResourceType != "" {
			scoped.Filters = append(append([]fhirsearch.Filter(nil), scoped.Filters...), pol.filters(req.ResourceType)...)
			scoped.Exprs = append(append([]*fhirsearch.Expr(nil), scoped.Exprs...), pol.exprs(req.ResourceType)...)
		} else {
			scoped.Types = intersect(scoped.Types, pol.Types(), req.Types)
			if len(scoped.Types) == 0 {
				return &fhirrouter.SearchResult{}, nil // Types rỗng nghĩa là "mọi loại" ở repository: không được chuyển tiếp
			}
		}
	}
	pf := fhirsearch.Filter{Code: fhir.ProjectParam.Code, Def: fhir.ProjectParam, Values: []string{project}}
	// Giữ cả điều kiện của chính sách đã thêm ở trên: đẩy xuống kho để total và phân trang chỉ tính phần người gọi được thấy (không lộ số lượng
	// resource ngoài chính sách). filterPolicy bên dưới vẫn lọc lại từng kết quả (kể cả _include) như lớp phòng thủ thứ hai.
	scoped.Filters = append(append([]fhirsearch.Filter(nil), scoped.Filters...), pf)
	res, err := r.inner.Search(ctx, &scoped)
	if err != nil {
		return nil, err
	}
	out := *res
	out.Matches = filterPolicy(filterReadable(filterProject(res.Matches, project), g), pol)
	out.Included = filterPolicy(filterReadable(filterProject(res.Included, project), g), pol)
	return &out, nil
}

func filterProject(in []fhir.Resource, project string) []fhir.Resource {
	var out []fhir.Resource
	for _, x := range in {
		if inProject(x, project) {
			out = append(out, x)
		}
	}
	return out
}

// intersect trả các loại thuộc cả chính sách lẫn (nếu có) danh sách đang giới hạn; luôn khác nil để không thành "mọi loại".
func intersect(restricted, policy, asked []string) []string {
	out := []string{}
	for _, t := range policy {
		if (len(restricted) == 0 || contains(restricted, t)) && (len(asked) == 0 || contains(asked, t)) {
			out = append(out, t)
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func filterPolicy(in []fhir.Resource, p *Policy) []fhir.Resource {
	if p == nil {
		return in
	}
	var out []fhir.Resource
	for _, x := range in {
		if p.matches(x) {
			out = append(out, x)
		}
	}
	return out
}

func filterReadable(in []fhir.Resource, g Grants) []fhir.Resource {
	if g == nil {
		return in
	}
	var out []fhir.Resource
	for _, x := range in {
		if g.CanSee(x.GetResourceType()) {
			out = append(out, x)
		}
	}
	return out
}

// RunInTransaction chạy fn với repository của giao dịch, cũng được tách theo project.
func (r *Repo) RunInTransaction(ctx context.Context, fn func(fhirrouter.Repository) error) error {
	tx, ok := r.inner.(fhirrouter.Transactional)
	if !ok {
		return fhirrouter.NotImplemented("Repository không hỗ trợ transaction")
	}
	return tx.RunInTransaction(ctx, func(rp fhirrouter.Repository) error { return fn(&Repo{inner: rp}) })
}

// LockKey chuyển cho repository bên trong, thêm project vào khóa để hai project không chặn nhau.
func (r *Repo) LockKey(ctx context.Context, key string) error {
	kl, ok := r.inner.(fhirrouter.KeyLocker)
	if !ok {
		return nil
	}
	project, err := projectOf(ctx)
	if err != nil {
		return err
	}
	return kl.LockKey(ctx, project+"|"+key)
}

func (r *Repo) Reindex(ctx context.Context, rt, id string, progress func(int)) (int, error) {
	project, err := projectOf(ctx)
	if err != nil {
		return 0, err
	}
	if err := allow(ctx, rt, PermUpdate); err != nil {
		return 0, err
	}
	if policyOf(ctx) != nil {
		return 0, fhirrouter.Forbidden("Reindex không dùng được với chính sách truy cập")
	}
	ri, ok := r.inner.(fhirrouter.Reindexer)
	if !ok {
		return 0, fhirrouter.NotImplemented("Repository không hỗ trợ reindex")
	}
	if id == "" && !grantsSuper(ctx) {
		return 0, fhirrouter.Forbidden("Reindex cả loại chạy xuyên project; dùng job quản trị")
	}
	if id != "" {
		if err := r.owned(ctx, project, rt, id); err != nil {
			return 0, err
		}
	}
	return ri.Reindex(ctx, rt, id, progress)
}

func (r *Repo) Expunge(ctx context.Context, rt, id string) error {
	project, err := projectOf(ctx)
	if err != nil {
		return err
	}
	if err := allow(ctx, rt, PermDelete); err != nil {
		return err
	}
	if policyOf(ctx) != nil {
		return fhirrouter.Forbidden("Expunge không dùng được với chính sách truy cập")
	}
	ex, ok := r.inner.(fhirrouter.Expunger)
	if !ok {
		return fhirrouter.NotImplemented("Repository không hỗ trợ expunge")
	}
	if err := r.ownedByHistory(ctx, project, rt, id); err != nil {
		return err
	}
	return ex.Expunge(ctx, rt, id)
}
