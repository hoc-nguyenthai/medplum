package fhirrouter_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type matchEntry struct {
	ID    string
	Score float64
	Grade string
}

func match(t *testing.T, env func(string, string, string, ...string) (int, string), body string) (int, []matchEntry, string) {
	t.Helper()
	code, out := env("POST", "Patient/$match", body)
	var b struct {
		Type  string `json:"type"`
		Total int    `json:"total"`
		Entry []struct {
			Resource struct{ ID string } `json:"resource"`
			Search   struct {
				Mode      string
				Score     float64
				Extension []struct{ URL, ValueCode string } `json:"extension"`
			} `json:"search"`
		} `json:"entry"`
	}
	_ = json.Unmarshal([]byte(out), &b)
	var res []matchEntry
	for _, e := range b.Entry {
		grade := ""
		for _, x := range e.Search.Extension {
			if strings.HasSuffix(x.URL, "match-grade") {
				grade = x.ValueCode
			}
		}
		if e.Search.Mode != "match" {
			t.Errorf("mode %q", e.Search.Mode)
		}
		res = append(res, matchEntry{e.Resource.ID, e.Search.Score, grade})
	}
	if code == 200 && (b.Type != "searchset" || b.Total != len(res)) {
		t.Errorf("Bundle: %s total %d / %d", b.Type, b.Total, len(res))
	}
	return code, res, out
}

func params(patient string, kv ...string) string {
	s := `{"resourceType":"Parameters","parameter":[{"name":"resource","resource":` + patient + `}`
	for i := 0; i+1 < len(kv); i += 2 {
		v := kv[i+1]
		if v == "true" || v == "false" {
			s += fmt.Sprintf(`,{"name":%q,"valueBoolean":%s}`, kv[i], v)
		} else {
			s += fmt.Sprintf(`,{"name":%q,"valueInteger":%s}`, kv[i], v)
		}
	}
	return s + `]}`
}

func TestPatientMatchGradesAndRanking(t *testing.T) {
	_, h := clinicalEnv(t)
	do := func(m, p, b string, _ ...string) (int, string) { return putTo(h, m, p, b) }
	put := func(id, body string) {
		mustPut(t, h, "Patient/"+id, `{"resourceType":"Patient","id":"`+id+`",`+body+`}`)
	}
	put("an", `"identifier":[{"system":"http://bhyt","value":"HC123"}],"name":[{"family":"Nguyễn","given":["An","Văn"]}],"gender":"male","birthDate":"1990-05-17","telecom":[{"system":"phone","value":"0901234567"}],"address":[{"line":["12 Lê Lợi"],"postalCode":"530000"}]`)
	put("an-fuzzy", `"name":[{"family":"Nguyen","given":["An"]}],"gender":"male","birthDate":"1990-05-18","address":[{"line":["12 Le Loi"],"postalCode":"530000"}]`)
	put("binh", `"name":[{"family":"Trần","given":["Bình"]}],"gender":"male","birthDate":"1985-01-01"`)
	put("an-nu", `"name":[{"family":"Nguyễn","given":["An"]}],"gender":"female","birthDate":"1990-05-17"`)
	put("an-jr", `"name":[{"family":"Nguyễn","given":["An"],"suffix":["Jr"]}],"gender":"male","birthDate":"1990-05-17"`)

	// identifier trùng cả hệ thống và giá trị: certain, điểm 1, xếp đầu
	code, res, out := match(t, do, params(`{"resourceType":"Patient","identifier":[{"system":"http://bhyt","value":"HC123"}]}`))
	if code != 200 || len(res) != 1 || res[0].ID != "an" || res[0].Grade != "certain" || res[0].Score != 1 {
		t.Fatalf("identifier: %d %+v %s", code, res, out)
	}
	// tên + ngày sinh + điện thoại trùng exact: certain; ứng viên fuzzy (không dấu, ngày lệch 1) vẫn xuất hiện nhưng thấp hơn
	in := `{"resourceType":"Patient","name":[{"family":"Nguyễn","given":["An"]}],"birthDate":"1990-05-17","gender":"male","telecom":[{"system":"phone","value":"0901234567"}],"address":[{"line":["12 Lê Lợi"]}]}`
	code, res, out = match(t, do, params(in))
	if code != 200 || len(res) < 2 || res[0].ID != "an" || res[0].Grade != "certain" {
		t.Fatalf("exact: %d %+v %s", code, res, out)
	}
	byID := map[string]matchEntry{}
	for _, r := range res {
		byID[r.ID] = r
	}
	if f, ok := byID["an-fuzzy"]; !ok || f.Grade == "certain" || f.Score >= 1 || f.Score <= 0.2 {
		t.Errorf("ứng viên fuzzy: %+v (có: %v)", f, res)
	}
	// khác giới tính và hậu tố thế hệ khác nhau bị loại; người khác hẳn không xuất hiện
	for _, id := range []string{"an-nu", "binh"} {
		if _, ok := byID[id]; ok {
			t.Errorf("%s không được xuất hiện: %+v", id, byID[id])
		}
	}
	// hậu tố thế hệ: chỉ loại khi CẢ HAI bên khai và khác nhau (Sr với Jr); một bên không khai thì vẫn là ứng viên
	if _, jr, _ := match(t, do, params(in)); true {
		found := false
		for _, r := range jr {
			found = found || r.ID == "an-jr"
		}
		if !found {
			t.Errorf("hậu tố chỉ một bên khai không được loại ứng viên: %+v", jr)
		}
	}
	srInput := strings.Replace(in, `"given":["An"]}`, `"given":["An"],"suffix":["Sr"]}`, 1)
	if _, sr, _ := match(t, do, params(srInput)); len(sr) > 0 {
		for _, r := range sr {
			if r.ID == "an-jr" {
				t.Errorf("Sr khác Jr phải bị loại: %+v", r)
			}
		}
	}
	// sắp theo điểm giảm dần và count giới hạn
	for i := 1; i < len(res); i++ {
		if res[i].Score > res[i-1].Score {
			t.Errorf("không sắp xếp theo điểm: %+v", res)
		}
	}
	if _, one, _ := match(t, do, params(in, "count", "1")); len(one) != 1 || one[0].ID != "an" {
		t.Errorf("count=1: %+v", one)
	}
	// Resource đưa thẳng (không bọc Parameters)
	if code, direct, _ := match(t, do, in); code != 200 || len(direct) < 1 || direct[0].ID != "an" {
		t.Errorf("body là Patient: %d %+v", code, direct)
	}
}

func TestPatientMatchOnlyCertainAndTwinGuard(t *testing.T) {
	_, h := clinicalEnv(t)
	do := func(m, p, b string, _ ...string) (int, string) { return putTo(h, m, p, b) }
	put := func(id, body string) {
		mustPut(t, h, "Patient/"+id, `{"resourceType":"Patient","id":"`+id+`",`+body+`}`)
	}
	// hai người trùng hệ thống và số điện thoại (certain mơ hồ) → onlyCertainMatches trả rỗng
	put("c1", `"name":[{"family":"Lê","given":["Cường"]}],"birthDate":"2000-02-02","telecom":[{"system":"phone","value":"0911"}]`)
	put("c2", `"name":[{"family":"Lê","given":["Cường"]}],"birthDate":"2000-02-02","telecom":[{"system":"phone","value":"0911"}]`)
	in := `{"resourceType":"Patient","name":[{"family":"Lê","given":["Cường"]}],"birthDate":"2000-02-02","telecom":[{"system":"phone","value":"0911"}]}`
	if _, res, _ := match(t, do, params(in)); len(res) != 2 {
		t.Fatalf("không giới hạn: %+v", res)
	}
	if _, res, _ := match(t, do, params(in, "onlyCertainMatches", "true")); len(res) != 0 {
		t.Errorf("certain mơ hồ phải rỗng: %+v", res)
	}
	// duy nhất một certain: trả đúng nó
	put("d1", `"identifier":[{"system":"http://x","value":"ONLY"}],"name":[{"family":"Phạm","given":["Dũng"]}]`)
	if _, res, _ := match(t, do, params(`{"resourceType":"Patient","identifier":[{"system":"http://x","value":"ONLY"}]}`, "onlyCertainMatches", "true")); len(res) != 1 || res[0].ID != "d1" {
		t.Errorf("certain duy nhất: %+v", res)
	}
	// sinh đôi cùng ngày sinh: tên gần giống (Bình/Bính) không được coi là cùng người nhờ chống nhầm
	put("t1", `"name":[{"family":"Hồ","given":["Bình"]}],"birthDate":"2010-10-10","gender":"male","address":[{"line":["1 A"]}]`)
	put("t2", `"name":[{"family":"Hồ","given":["Bính"]}],"birthDate":"2010-10-10","gender":"male","address":[{"line":["1 A"]}]`)
	_, res, _ := match(t, do, params(`{"resourceType":"Patient","name":[{"family":"Hồ","given":["Bình"]}],"birthDate":"2010-10-10","gender":"male","address":[{"line":["1 A"]}]}`))
	var t1, t2 matchEntry
	for _, r := range res {
		switch r.ID {
		case "t1":
			t1 = r
		case "t2":
			t2 = r
		}
	}
	if t1.ID == "" || t1.Score <= t2.Score || (t2.ID != "" && t2.Score >= t1.Score) {
		t.Errorf("chống sinh đôi: t1=%+v t2=%+v", t1, t2)
	}
}

func TestPatientMatchErrors(t *testing.T) {
	_, h := clinicalEnv(t)
	for name, c := range map[string]struct{ body string }{
		"không phải Patient":  {`{"resourceType":"Parameters","parameter":[{"name":"resource","resource":{"resourceType":"Observation","status":"final","code":{"text":"x"}}}]}`},
		"thiếu resource":      {`{"resourceType":"Parameters","parameter":[]}`},
		"không có trường nào": {params(`{"resourceType":"Patient","gender":"male"}`)},
		"count không hợp lệ":  {params(`{"resourceType":"Patient","birthDate":"2000-01-01"}`, "count", "0")},
	} {
		if code, out := putTo(h, "POST", "Patient/$match", c.body); code != 400 {
			t.Errorf("%s: %d %s", name, code, out)
		}
	}
	if code, res, _ := match(t, func(m, p, b string, _ ...string) (int, string) { return putTo(h, m, p, b) }, params(`{"resourceType":"Patient","birthDate":"1900-01-01"}`)); code != 200 || len(res) != 0 {
		t.Errorf("không có ứng viên: %d %+v", code, res)
	}
}

func TestPatientMatchScoringDetails(t *testing.T) {
	near := func(got, want float64) bool { return got > want-1e-9 && got < want+1e-9 }
	newEnv := func() func(string, string, string, ...string) (int, string) {
		_, h := clinicalEnv(t)
		return func(m, p, b string, _ ...string) (int, string) { return putTo(h, m, p, b) }
	}
	_ = newEnv
	// tên ngắn bỏ dấu (Ân/An) là fuzzy; điểm = (họ 1 + ngày sinh 1 + tên 0,5) / 6
	_, h := clinicalEnv(t)
	do := func(m, p, b string, _ ...string) (int, string) { return putTo(h, m, p, b) }
	mustPut(t, h, "Patient/short", `{"resourceType":"Patient","id":"short","name":[{"family":"Vũ","given":["An"]}],"birthDate":"1999-09-09"}`)
	_, res, _ := match(t, do, params(`{"resourceType":"Patient","name":[{"family":"Vũ","given":["Ân"]}],"birthDate":"1999-09-09"}`))
	if len(res) != 1 || !near(res[0].Score, 2.5/6) || res[0].Grade != "possible" {
		t.Errorf("fuzzy bỏ dấu tên ngắn: %+v", res)
	}
	// chỉ họ và ngày sinh trùng: possible (2/6 = 0,33), dưới ngưỡng probable
	mustPut(t, h, "Patient/poss", `{"resourceType":"Patient","id":"poss","name":[{"family":"Đỗ","given":["Khác"]}],"birthDate":"1980-01-01"}`)
	_, res, _ = match(t, do, params(`{"resourceType":"Patient","name":[{"family":"Đỗ","given":["Nhau"]}],"birthDate":"1980-01-01"}`))
	if len(res) != 1 || res[0].ID != "poss" || res[0].Grade != "possible" || !near(res[0].Score, 2.0/6) {
		t.Errorf("possible: %+v", res)
	}
	// điểm không chắc chắn bị chặn trần 0,9 (tên fuzzy nên không phải certain)
	_, h2 := clinicalEnv(t)
	do2 := func(m, p, b string, _ ...string) (int, string) { return putTo(h2, m, p, b) }
	mustPut(t, h2, "Patient/cap", `{"resourceType":"Patient","id":"cap","name":[{"family":"Hồ","given":["Bính"]}],"birthDate":"2010-10-10","gender":"male","telecom":[{"system":"phone","value":"0988"}],"address":[{"line":["1 A"],"postalCode":"70000"}]}`)
	_, res, _ = match(t, do2, params(`{"resourceType":"Patient","name":[{"family":"Hồ","given":["Bình"]}],"birthDate":"2010-10-10","gender":"male","telecom":[{"system":"phone","value":"0988"}],"address":[{"line":["1 A"],"postalCode":"70000"}]}`))
	if len(res) != 1 || res[0].Grade != "probable" || !near(res[0].Score, 0.9) {
		t.Errorf("trần 0,9: %+v", res)
	}
	// chống sinh đôi: hai ứng viên cùng ngày sinh thì tên đầu phải exact; một ứng viên duy nhất thì tên fuzzy vẫn được cộng điểm
	_, h3 := clinicalEnv(t)
	do3 := func(m, p, b string, _ ...string) (int, string) { return putTo(h3, m, p, b) }
	mustPut(t, h3, "Patient/t2", `{"resourceType":"Patient","id":"t2","name":[{"family":"Hồ","given":["Bính"]}],"birthDate":"2010-10-10","gender":"male","address":[{"line":["1 A"]}]}`)
	in := `{"resourceType":"Patient","name":[{"family":"Hồ","given":["Bình"]}],"birthDate":"2010-10-10","gender":"male","address":[{"line":["1 A"]}]}`
	_, alone, _ := match(t, do3, params(in))
	mustPut(t, h3, "Patient/t1", `{"resourceType":"Patient","id":"t1","name":[{"family":"Hồ","given":["Bình"]}],"birthDate":"2010-10-10","gender":"male","address":[{"line":["1 A"]}]}`)
	_, twins, _ := match(t, do3, params(in))
	var t2alone, t2twin float64
	for _, r := range alone {
		if r.ID == "t2" {
			t2alone = r.Score
		}
	}
	for _, r := range twins {
		if r.ID == "t2" {
			t2twin = r.Score
		}
	}
	if !near(t2alone, 4.5/6) || !near(t2twin, 4.0/6) {
		t.Errorf("sinh đôi: một mình %v (muốn 0,75), có anh em %v (muốn 0,6667)", t2alone, t2twin)
	}
	// ứng viên yếu được tìm thấy trước nhưng phải xếp sau
	_, h4 := clinicalEnv(t)
	do4 := func(m, p, b string, _ ...string) (int, string) { return putTo(h4, m, p, b) }
	mustPut(t, h4, "Patient/aaa-weak", `{"resourceType":"Patient","id":"aaa-weak","name":[{"family":"Lý","given":["Nhật"]}],"birthDate":"2001-01-01"}`)
	mustPut(t, h4, "Patient/zzz-strong", `{"resourceType":"Patient","id":"zzz-strong","name":[{"family":"Lý","given":["Hạ"]}],"birthDate":"2001-01-01","telecom":[{"system":"email","value":"ly@example.org"}]}`)
	_, res, _ = match(t, do4, params(`{"resourceType":"Patient","name":[{"family":"Lý","given":["Hạ"]}],"birthDate":"2001-01-01","telecom":[{"system":"email","value":"LY@example.org"}]}`))
	if len(res) != 2 || res[0].ID != "zzz-strong" || res[1].ID != "aaa-weak" || res[0].Grade != "certain" {
		t.Errorf("thứ tự và certain nhờ email không phân biệt hoa thường: %+v", res)
	}
}
