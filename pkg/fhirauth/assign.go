package fhirauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirsearch"
)

// AssignReport là kết quả của AssignProject.
type AssignReport struct {
	Scanned  int            // số resource đã quét
	Assigned map[string]int // số resource được (hoặc sẽ được, khi DryRun) gán project, theo loại
	Failed   int            // số resource không gán được (xem lỗi trả về)
}

// Total là tổng số resource được gán.
func (r AssignReport) Total() int {
	n := 0
	for _, c := range r.Assigned {
		n += c
	}
	return n
}

// AssignProject gán Meta.project cho các resource chưa có project (dữ liệu ghi trước khi bật tách project; không
// project nào nhìn thấy chúng). Resource đã có project (kể cả project khác) không bị đụng. Mỗi lần gán tạo một phiên
// bản mới. Dùng repository GỐC (không bọc Scope). Idempotent: chạy lại chỉ xử lý phần còn lại.
//
// types giới hạn các loại cần quét (rỗng: mọi loại, trừ AsyncJob). dryRun chỉ đếm, không ghi. Nên chạy khi không có
// ghi đồng thời; resource bị sửa giữa lúc quét và lúc gán bị bỏ qua (If-Match) và tính vào Failed, chạy lại để xử lý nốt.
func AssignProject(ctx context.Context, repo fhirrouter.Repository, project string, types []string, dryRun bool) (AssignReport, error) {
	rep := AssignReport{Assigned: map[string]int{}}
	if project == "" {
		return rep, errors.New("fhirauth: thiếu project cần gán")
	}
	if len(types) == 0 {
		for _, rt := range fhir.ResourceTypes {
			if rt != jobType {
				types = append(types, rt)
			}
		}
	}
	const page = 500
	var firstErr error
	for _, rt := range types {
		if !knownTypes[rt] || rt == "*" {
			return rep, fmt.Errorf("fhirauth: loại resource không biết: %q", rt)
		}
		// lượt 1: gom id các resource chưa có project (không ghi trong lúc phân trang để offset ổn định)
		type legacy struct{ id, version string }
		var todo []legacy
		for offset := 0; ; offset += page {
			req, err := fhirsearch.Parse(rt, url.Values{"_sort": {"_lastUpdated"}})
			if err != nil {
				return rep, err
			}
			req.Count, req.Offset, req.Total = page, offset, "none"
			res, err := repo.Search(ctx, req)
			if err != nil {
				return rep, fmt.Errorf("quét %s: %w", rt, err)
			}
			for _, m := range res.Matches {
				rep.Scanned++
				if meta := m.GetMeta(); meta == nil || meta.Project == "" {
					v := ""
					if meta != nil {
						v = meta.VersionId
					}
					todo = append(todo, legacy{m.GetID(), v})
				}
			}
			if !res.More {
				break
			}
		}
		// lượt 2: gán
		for _, l := range todo {
			if err := ctx.Err(); err != nil {
				return rep, err
			}
			if dryRun {
				rep.Assigned[rt]++
				continue
			}
			cur, err := repo.ReadResource(ctx, rt, l.id)
			if err == nil {
				if m := cur.GetMeta(); m != nil && m.Project != "" {
					continue // đã có người gán trong lúc chạy
				}
				var s fhir.Resource
				if s, err = stamp(cur, project); err == nil {
					_, err = repo.UpdateResource(ctx, s, fhirrouter.UpdateOptions{IfMatch: l.version})
				}
			}
			if err != nil {
				rep.Failed++
				if firstErr == nil {
					firstErr = fmt.Errorf("%s/%s: %w", rt, l.id, err)
				}
				continue
			}
			rep.Assigned[rt]++
		}
	}
	if firstErr != nil {
		return rep, fmt.Errorf("có %d resource không gán được, lỗi đầu tiên: %w", rep.Failed, firstErr)
	}
	return rep, nil
}
