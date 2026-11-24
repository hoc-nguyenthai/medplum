package bootstrap_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// hiddenFields, readonlyFields và interaction của AccessPolicy qua server thật (token JWT mang claim fhir_access_policy): gỡ trường ẩn khỏi đọc, vread, history, search và kết quả ghi;
// chặn ghi trường chỉ-đọc và khôi phục trường ẩn khi PUT và PATCH; giới hạn interaction.
func TestAccessPolicyFieldRulesEndToEnd(t *testing.T) {
	_, srv := newServer(t, jwtSettings())
	full := tok(t, "A", "fhir.write")
	body := map[string]any{"resourceType": "Patient", "gender": "female", "birthDate": "1990-01-01",
		"name": []any{map[string]any{"family": "Nguyễn", "given": []any{"An"}}}, "telecom": []any{map[string]any{"system": "phone", "value": "0901"}}}
	id := want(t, call(t, srv, "POST", "/fhir/R4/Patient", full, body), 201).str(t, "id")

	pol := func(rules ...any) string {
		return tok(t, "A", "fhir.write", func(c jwt.MapClaims) { c["fhir_access_policy"] = rules })
	}
	fields := pol(map[string]any{"resourceType": "Patient", "hiddenFields": []any{"birthDate", "name.family"}, "readonlyFields": []any{"gender"}})

	// mọi đường đọc đều không lộ trường ẩn
	vid := want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, full, nil), 200).str(t, "meta", "versionId")
	for name, path := range map[string]string{
		"read": "/fhir/R4/Patient/" + id, "vread": "/fhir/R4/Patient/" + id + "/_history/" + vid, "history": "/fhir/R4/Patient/" + id + "/_history",
		"search": "/fhir/R4/Patient", "everything": "/fhir/R4/Patient/" + id + "/$everything",
	} {
		out := string(want(t, call(t, srv, "GET", path, fields, nil), 200).body)
		if strings.Contains(out, "birthDate") || strings.Contains(out, "Nguyễn") || strings.Contains(out, "1990") {
			t.Errorf("%s lộ trường ẩn: %.300s", name, out)
		}
		if !strings.Contains(out, "0901") || !strings.Contains(out, `"An"`) {
			t.Errorf("%s mất trường không ẩn: %.300s", name, out)
		}
	}
	if out := string(want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, full, nil), 200).body); !strings.Contains(out, "1990-01-01") {
		t.Fatalf("token đầy đủ vẫn phải thấy: %s", out)
	}

	// PUT: người gọi gửi lại bản đã ẩn kèm đổi trường readonly và thử đặt trường ẩn: bản lưu giữ giá trị cũ
	put := map[string]any{"resourceType": "Patient", "id": id, "gender": "male", "birthDate": "1800-01-01",
		"name": []any{map[string]any{"given": []any{"Bình"}}}, "telecom": []any{map[string]any{"system": "phone", "value": "0999"}}}
	out := want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, fields, put), 200)
	if strings.Contains(string(out.body), "birthDate") {
		t.Errorf("kết quả PUT lộ trường ẩn: %s", out.body)
	}
	stored := want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, full, nil), 200).json(t)
	name0 := stored["name"].([]any)[0].(map[string]any)
	if stored["gender"] != "female" || stored["birthDate"] != "1990-01-01" || name0["given"].([]any)[0] != "Bình" || stored["telecom"].([]any)[0].(map[string]any)["value"] != "0999" {
		t.Errorf("sau PUT: %v", stored)
	}
	// trường ẩn lồng (`name.family`) cũng được khôi phục từ bản cũ dù người gọi không thấy nó (trước đây bị mất khi họ ghi lại tên)
	if name0["family"] != "Nguyễn" {
		t.Errorf("trường ẩn lồng name.family bị mất sau PUT: %v", name0)
	}

	// tìm kiếm, sắp xếp, _filter theo trường ẩn bị từ chối: nếu không thì suy ra được giá trị bằng truy vấn
	for name, q := range map[string]string{
		"lọc": "birthdate=1990-01-01", "sắp xếp": "_sort=-birthdate", "họ": "family=Nguy%E1%BB%85n", "_filter": "_filter=birthdate%20eq%201990-01-01", "_content": "_content=Nguy%E1%BB%85n",
	} {
		if r := call(t, srv, "GET", "/fhir/R4/Patient?"+q, fields, nil); r.status != 403 {
			t.Errorf("%s theo trường ẩn: %d, muốn 403 (%s)", name, r.status, r.body)
		}
	}
	want(t, call(t, srv, "GET", "/fhir/R4/Patient?given=An&_sort=gender", fields, nil), 200) // tham số không dính trường ẩn vẫn dùng được
	want(t, call(t, srv, "GET", "/fhir/R4/Patient?birthdate=1990-01-01", full, nil), 200)    // token không có quy tắc ẩn tìm bình thường

	// PATCH đi qua đọc-sửa-ghi: không đổi được readonly, không làm mất trường ẩn cấp cao nhất
	patch := []any{map[string]any{"op": "replace", "path": "/gender", "value": "other"}, map[string]any{"op": "replace", "path": "/telecom/0/value", "value": "0888"}}
	want(t, callCT(t, srv, "PATCH", "/fhir/R4/Patient/"+id, fields, "application/json-patch+json", patchJSON(patch)), 200)
	stored = want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, full, nil), 200).json(t)
	if stored["gender"] != "female" || stored["birthDate"] != "1990-01-01" || stored["telecom"].([]any)[0].(map[string]any)["value"] != "0888" {
		t.Errorf("sau PATCH: %v", stored)
	}

	// create qua chính sách: trường readonly và hidden bị bỏ
	c := want(t, call(t, srv, "POST", "/fhir/R4/Patient", fields, body), 201)
	if strings.Contains(string(c.body), "birthDate") {
		t.Errorf("kết quả create lộ trường ẩn: %s", c.body)
	}
	cid := c.str(t, "id")
	if s := string(want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+cid, full, nil), 200).body); strings.Contains(s, "birthDate") || strings.Contains(s, "gender") {
		t.Errorf("create phải bỏ readonly/hidden khỏi bản lưu: %s", s)
	}

	// interaction
	roSearch := pol(map[string]any{"resourceType": "Patient", "interaction": []any{"read", "search"}})
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, roSearch, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", roSearch, nil), 200)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id+"/_history", roSearch, nil), 403)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id+"/_history/"+vid, roSearch, nil), 403)
	want(t, call(t, srv, "PUT", "/fhir/R4/Patient/"+id, roSearch, put), 403)
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", roSearch, body), 403)
	want(t, call(t, srv, "DELETE", "/fhir/R4/Patient/"+id, roSearch, nil), 403)
	onlyCreate := pol(map[string]any{"resourceType": "Patient", "interaction": []any{"create"}})
	want(t, call(t, srv, "POST", "/fhir/R4/Patient", onlyCreate, body), 201)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient/"+id, onlyCreate, nil), 403)
	// cấu hình sai: claim có tên trường lạ thì token bị từ chối (fail đóng)
	want(t, call(t, srv, "GET", "/fhir/R4/Patient", pol(map[string]any{"resourceType": "Patient", "hiddenFields": []any{"name; drop"}}), nil), 403)
}

func patchJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
