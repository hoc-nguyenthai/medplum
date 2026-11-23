package fhirstore_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"trivita-fhir/pkg/fhirrouter"
)

// Benchmark đầu-cuối của Router + Store (không qua mạng) trên dữ liệu có kích thước thật:
//
//	go test ./pkg/fhirstore -run '^$' -bench FHIR -benchtime 2s                  # SQLite
//	FHIRSTORE_TEST_POSTGRES_URL=... go test ./pkg/fhirstore -run '^$' -bench FHIR  # thêm PostgreSQL
//
// Cỡ dữ liệu: BENCH_PATIENTS (mặc định 5000), mỗi bệnh nhân 2 Observation. Số liệu chỉ có ý nghĩa tương đối (máy phát triển,
// cơ sở dữ liệu cùng máy, một kết nối client).

var benchFamilies = []string{"Nguyen", "Tran", "Le", "Pham", "Hoang", "Vu", "Dang", "Bui", "Do", "Ngo"}

func benchPatients() int {
	if n, err := strconv.Atoi(os.Getenv("BENCH_PATIENTS")); err == nil && n > 0 {
		return n
	}
	return 5000
}

type benchEnv struct {
	h http.Handler
	n int
}

func (e *benchEnv) do(b testing.TB, method, path string, body any, hdr ...string) int {
	var rd *strings.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = strings.NewReader(string(raw))
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/fhir/R4/"+path, rd)
	req.Header.Set("Content-Type", "application/fhir+json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code
}

func seedBench(b *testing.B, repo fhirrouter.Repository) *benchEnv {
	b.Helper()
	r := fhirrouter.New(repo, fhirrouter.Options{BaseURL: "http://bench/fhir/R4", UpdateAsCreate: true, SkipValidation: true})
	e := &benchEnv{h: http.StripPrefix("/fhir/R4", r.HTTPHandler()), n: benchPatients()}
	const batch = 200
	var entries []any
	flush := func() {
		if len(entries) == 0 {
			return
		}
		if code := e.do(b, "POST", "", map[string]any{"resourceType": "Bundle", "type": "batch", "entry": entries}); code != 200 {
			b.Fatalf("seed: %d", code)
		}
		entries = entries[:0]
	}
	put := func(rt, id string, res map[string]any) {
		res["resourceType"], res["id"] = rt, id
		entries = append(entries, map[string]any{"resource": res, "request": map[string]any{"method": "PUT", "url": rt + "/" + id}})
		if len(entries) >= batch {
			flush()
		}
	}
	for i := 0; i < e.n; i++ {
		put("Patient", "p"+strconv.Itoa(i), map[string]any{
			"name":      []any{map[string]any{"family": benchFamilies[i%len(benchFamilies)], "given": []any{"G" + strconv.Itoa(i)}}},
			"gender":    []string{"male", "female"}[i%2],
			"birthDate": fmt.Sprintf("%d-%02d-%02d", 1950+i%60, 1+i%12, 1+i%28),
		})
		for k := 0; k < 2; k++ {
			code := "c" + strconv.Itoa((i+k)%10)
			put("Observation", "o"+strconv.Itoa(i)+"-"+strconv.Itoa(k), map[string]any{
				"status":  "final",
				"code":    map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": code}}},
				"subject": map[string]any{"reference": "Patient/p" + strconv.Itoa(i)},
				"component": []any{
					map[string]any{"code": map[string]any{"coding": []any{map[string]any{"system": "http://loinc.org", "code": "8480-6"}}},
						"valueQuantity": map[string]any{"value": 90 + i%80, "unit": "mmHg", "system": "http://unitsofmeasure.org", "code": "mm[Hg]"}},
				},
			})
		}
	}
	flush()
	return e
}

func BenchmarkFHIR(b *testing.B) {
	backends := map[string]func(testing.TB) fhirrouter.Repository{
		"sqlite": func(t testing.TB) fhirrouter.Repository { return newSQLite(t) },
	}
	if os.Getenv(PostgresEnv) != "" {
		backends["postgres"] = func(t testing.TB) fhirrouter.Repository { return newPostgres(t) }
	}
	for _, name := range []string{"sqlite", "postgres"} {
		mk, ok := backends[name]
		if !ok {
			continue
		}
		b.Run(name, func(b *testing.B) {
			e := seedBench(b, mk(b))
			var ctr atomic.Int64
			pick := func() int { return int(ctr.Add(1)) % e.n }

			get := func(name, path string) {
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						if code := e.do(b, "GET", strings.ReplaceAll(path, "{i}", strconv.Itoa(pick())), nil); code != 200 {
							b.Fatalf("%s: %d", path, code)
						}
					}
				})
			}
			get("ReadByID", "Patient/p{i}")
			get("SearchFamilyExact", "Patient?family=Nguyen&_count=20")
			get("SearchFamilyAccurateTotal", "Patient?family=Nguyen&_count=20&_total=accurate")
			get("SearchFamilyPrefixContains", "Patient?family:contains=gu&_count=20")
			get("SearchGivenByID", "Patient?given=G{i}")
			get("SearchTokenGender", "Patient?gender=female&_count=20")
			get("SearchDateRange", "Patient?birthdate=ge1970-01-01&birthdate=lt1975-01-01&_count=20")
			get("SearchSortBirthdate", "Patient?_sort=birthdate&_count=20")
			get("SearchSortDescPage3", "Patient?_sort=-birthdate&_count=20&_offset=40")
			get("SearchObservationBySubject", "Observation?subject=Patient/p{i}")
			get("SearchObservationByCode", "Observation?code=http://loinc.org|c3&_count=20")
			get("SearchChained", "Observation?subject:Patient.family=Tran&_count=20")
			get("SearchHas", "Patient?_has:Observation:subject:code=c3&_count=20")
			get("SearchComposite", "Observation?component-code-value-quantity=8480-6$gt150&_count=20")
			get("SearchInclude", "Observation?code=c3&_include=Observation:subject&_count=20")
			get("SearchFilterExpr", `Patient?_filter=gender+eq+female+and+family+eq+Tran&_count=20`)
			get("History", "Patient/p{i}/_history")

			b.Run("CreatePatient", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if code := e.do(b, "POST", "Patient", map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "Bench"}}}); code != 201 {
						b.Fatalf("create: %d", code)
					}
				}
			})
			b.Run("UpdatePatient", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					id := "p" + strconv.Itoa(pick())
					if code := e.do(b, "PUT", "Patient/"+id, map[string]any{"resourceType": "Patient", "id": id, "gender": "male", "name": []any{map[string]any{"family": "Upd"}}}); code != 200 {
						b.Fatalf("update: %d", code)
					}
				}
			})
			b.Run("ConditionalCreateExisting", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if code := e.do(b, "POST", "Patient", map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "Bench"}}}, "If-None-Exist", "family=Bench&_count=1"); code != 200 && code != 412 {
						b.Fatalf("cond create: %d", code)
					}
				}
			})
			b.Run("TransactionBundle10", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					var es []any
					for k := 0; k < 10; k++ {
						es = append(es, map[string]any{"resource": map[string]any{"resourceType": "Patient", "name": []any{map[string]any{"family": "Tx"}}}, "request": map[string]any{"method": "POST", "url": "Patient"}})
					}
					if code := e.do(b, "POST", "", map[string]any{"resourceType": "Bundle", "type": "transaction", "entry": es}); code != 200 {
						b.Fatalf("tx: %d", code)
					}
				}
			})
			b.Run("ParallelRead", func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if code := e.do(b, "GET", "Patient/p"+strconv.Itoa(pick()), nil); code != 200 {
							b.Errorf("read: %d", code)
							return
						}
					}
				})
			})
			b.Run("ParallelSearch", func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if code := e.do(b, "GET", "Observation?subject=Patient/p"+strconv.Itoa(pick()), nil); code != 200 {
							b.Errorf("search: %d", code)
							return
						}
					}
				})
			})
		})
	}
}
