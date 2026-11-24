package dicomretrieve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"trivita-fhir/pkg/dicomweb"
	"trivita-fhir/pkg/fhir"
)

// Scope giới hạn truy vấn trong một study (và series) cụ thể; rỗng là cả project.
type Scope struct{ StudyUID, SeriesUID string }

// maxParents là số study hoặc series tối đa được tìm trước để thu hẹp truy vấn cấp dưới (tìm series theo PatientID: trước hết tìm study khớp). Nhiều hơn thì bộ lọc quá rộng.
const maxParents = 1000

// maxPostScan là số series tối đa được đọc khi phải lọc sau theo Modality.
const maxPostScan = 10000

func refList(prefix string, ids []string) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = prefix + id
	}
	return strings.Join(out, ",")
}

func clone(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func (c Config) studies(ctx context.Context, params url.Values, limit, offset int) ([]*fhir.DicomStudy, bool, *Error) {
	p := clone(params)
	p.Set("_sort", "-_lastUpdated")
	res, err := search(ctx, c.Repo, "DicomStudy", p, limit, offset)
	if err != nil {
		return nil, false, internalErr(ctx, "tìm study", err)
	}
	var out []*fhir.DicomStudy
	for _, m := range res.Matches {
		if s, ok := m.(*fhir.DicomStudy); ok {
			out = append(out, s)
		}
	}
	return out, res.More, nil
}

// parentStudies trả id các study khớp bộ lọc cấp study (hoặc study của scope); nil, false nếu không có ràng buộc nào ở cấp study.
func (c Config) parentStudies(ctx context.Context, sc Scope, q *Query) (ids []string, constrained bool, e *Error) {
	if sc.StudyUID != "" {
		st, e := c.study(ctx, sc.StudyUID)
		if e != nil {
			return nil, true, e
		}
		return []string{st.ID}, true, nil
	}
	if len(q.Study) == 0 {
		return nil, false, nil
	}
	list, more, e := c.studies(ctx, q.Study, maxParents, 0)
	if e != nil {
		return nil, true, e
	}
	if more {
		return nil, true, &Error{http.StatusBadRequest, "Bộ lọc cấp study quá rộng (hơn 1000 study khớp)"}
	}
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	return ids, true, nil
}

// seriesPage đọc một trang series theo ràng buộc cha và bộ lọc series; lọc sau theo Modality nếu có.
func (c Config) seriesList(ctx context.Context, studyIDs []string, studyConstrained bool, sc Scope, q *Query, limit, offset int) ([]*fhir.DicomSeries, bool, *Error) {
	if studyConstrained && len(studyIDs) == 0 {
		return nil, false, nil
	}
	p := clone(q.Series)
	if studyConstrained {
		p.Set("study", refList("DicomStudy/", studyIDs))
	}
	if sc.SeriesUID != "" {
		p.Set("series-instance-uid:exact", sc.SeriesUID)
	}
	collect := func(res []fhir.Resource) []*fhir.DicomSeries {
		var out []*fhir.DicomSeries
		for _, m := range res {
			if s, ok := m.(*fhir.DicomSeries); ok {
				out = append(out, s)
			}
		}
		return out
	}
	if len(q.Modality) == 0 {
		res, err := search(ctx, c.Repo, "DicomSeries", p, limit, offset)
		if err != nil {
			return nil, false, internalErr(ctx, "tìm series", err)
		}
		return collect(res.Matches), res.More, nil
	}
	// lọc sau: đọc từng trang tới khi đủ (offset + limit + 1) kết quả khớp hoặc hết dữ liệu
	want := map[string]bool{}
	for _, m := range q.Modality {
		want[m] = true
	}
	var matched []*fhir.DicomSeries
	scanned := 0
	for page := 0; scanned < maxPostScan; page++ {
		res, err := search(ctx, c.Repo, "DicomSeries", p, MaxLimit, page*MaxLimit)
		if err != nil {
			return nil, false, internalErr(ctx, "tìm series", err)
		}
		for _, s := range collect(res.Matches) {
			scanned++
			if want[strings.ToUpper(strings.TrimSpace(s.Modality))] {
				matched = append(matched, s)
			}
		}
		if !res.More || len(matched) > offset+limit {
			break
		}
	}
	if offset >= len(matched) {
		return nil, false, nil
	}
	end := min(offset+limit, len(matched))
	return matched[offset:end], len(matched) > end, nil
}

// Search là QIDO-RS ở một cấp: lọc theo thuộc tính của cấp đó và của các cấp trên, trong phạm vi sc. Study mới cập nhật trước; series và instance theo thứ tự ổn định của kho.
// Study (hoặc series) của sc không có thì 404.
func Search(ctx context.Context, c Config, level Level, sc Scope, q *Query) (*Page, error) {
	if q == nil {
		q = &Query{}
	}
	if q.Study == nil {
		q.Study, q.Series, q.Instance = url.Values{}, url.Values{}, url.Values{}
	}
	limit, offset := clamp(q.Limit), max(q.Offset, 0)
	page := &Page{Items: []dicomweb.Dataset{}, Ignored: q.Ignored}

	switch level {
	case LevelStudy:
		list, more, e := c.studies(ctx, q.Study, limit, offset)
		if e != nil {
			return nil, e
		}
		for _, s := range list {
			page.Items = append(page.Items, dicomweb.StudyToDataset(s))
		}
		page.More = more
		return page, nil

	case LevelSeries:
		studyIDs, constrained, e := c.parentStudies(ctx, sc, q)
		if e != nil {
			return nil, e
		}
		list, more, e := c.seriesList(ctx, studyIDs, constrained, sc, q, limit, offset)
		if e != nil {
			return nil, e
		}
		studies := map[string]*fhir.DicomStudy{}
		for _, se := range list {
			if se.Study == nil {
				continue
			}
			id := strings.TrimPrefix(se.Study.Reference, "DicomStudy/")
			st := studies[id]
			if st == nil {
				r, err := c.Repo.ReadResource(ctx, "DicomStudy", id)
				if err != nil {
					return nil, internalErr(ctx, "đọc study của series", err)
				}
				st, _ = r.(*fhir.DicomStudy)
				studies[id] = st
			}
			if st != nil {
				page.Items = append(page.Items, dicomweb.SeriesToDataset(st, se))
			}
		}
		page.More = more
		return page, nil

	default: // LevelInstance
		studyIDs, studyConstrained, e := c.parentStudies(ctx, sc, q)
		if e != nil {
			return nil, e
		}
		p := clone(q.Instance)
		switch {
		case sc.SeriesUID != "" || len(q.Series) > 0 || len(q.Modality) > 0:
			// ràng buộc ở cấp series: tìm series khớp rồi lọc instance theo các series đó
			var seriesIDs []string
			for off := 0; ; off += maxParents {
				list, more, e := c.seriesList(ctx, studyIDs, studyConstrained, sc, q, maxParents, off)
				if e != nil {
					return nil, e
				}
				for _, se := range list {
					seriesIDs = append(seriesIDs, se.ID)
				}
				if !more {
					break
				}
				if len(seriesIDs) >= maxParents {
					return nil, &Error{http.StatusBadRequest, "Bộ lọc cấp series quá rộng (hơn 1000 series khớp)"}
				}
			}
			if sc.SeriesUID != "" && len(seriesIDs) == 0 {
				return nil, notFound("Series not found")
			}
			if len(seriesIDs) == 0 {
				return page, nil
			}
			p.Set("series", refList("DicomSeries/", seriesIDs))
		case studyConstrained:
			if len(studyIDs) == 0 {
				return page, nil
			}
			p.Set("study", refList("DicomStudy/", studyIDs))
		}
		res, err := search(ctx, c.Repo, "DicomInstance", p, limit, offset)
		if err != nil {
			return nil, internalErr(ctx, "tìm instance", err)
		}
		for _, m := range res.Matches {
			if inst, ok := m.(*fhir.DicomInstance); ok && inst.Metadata != "" && json.Valid([]byte(inst.Metadata)) {
				page.Raw = append(page.Raw, json.RawMessage(inst.Metadata))
			}
		}
		page.More = res.More
		return page, nil
	}
}
