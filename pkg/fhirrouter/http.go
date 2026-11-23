package fhirrouter

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"trivita-fhir/pkg/fhir"
)

// MaxBodyBytes là kích thước body tối đa HTTPHandler chấp nhận.
const MaxBodyBytes = 16 << 20

const fhirJSON = "application/fhir+json; charset=utf-8"

// HTTPHandler trả về http.Handler cho router. Đường dẫn được tính từ URL.Path sau khi người gọi bỏ
// tiền tố bằng http.StripPrefix, ví dụ:
//
//	mux.Handle("/fhir/R4/", http.StripPrefix("/fhir/R4", router.HTTPHandler()))
//
// Echo: e.Any("/fhir/R4/*", echo.WrapHandler(http.StripPrefix("/fhir/R4", router.HTTPHandler()))).
func (r *Router) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, hr *http.Request) {
		req, err := readRequest(hr)
		if err != nil {
			writeError(w, asError(err))
			return
		}
		res, err := r.Handle(hr.Context(), req)
		if err != nil {
			writeError(w, asError(err))
			return
		}
		writeResponse(w, req, res)
	})
}

func readRequest(hr *http.Request) (*Request, error) {
	req := &Request{
		Method: hr.Method,
		Path:   strings.Trim(hr.URL.Path, "/"),
		Query:  hr.URL.Query(),
		Header: hr.Header,
	}
	raw, err := io.ReadAll(http.MaxBytesReader(nil, hr.Body, MaxBodyBytes))
	if err != nil {
		return nil, newError(413, "too-costly", "Body quá lớn")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return req, nil
	}
	mt, _, _ := mime.ParseMediaType(hr.Header.Get("Content-Type"))
	switch {
	case mt == "application/x-www-form-urlencoded":
		form, err := url.ParseQuery(string(raw))
		if err != nil {
			return nil, BadRequest("Form không hợp lệ")
		}
		for k, vs := range form {
			for _, v := range vs {
				req.Query.Add(k, v)
			}
		}
	case mt == "application/graphql":
		req.Body = string(raw)
	case mt == "" || mt == "application/json" || strings.HasSuffix(mt, "+json"):
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			return nil, BadRequest("JSON không hợp lệ: " + err.Error())
		}
		req.Body = v
	default:
		return nil, newError(415, "not-supported", "Content-Type không hỗ trợ: "+mt)
	}
	return req, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, "lỗi mã hóa JSON", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", fhirJSON)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeError(w http.ResponseWriter, e *Error) {
	out := e.Outcome
	if out == nil {
		out = newError(e.Status, "exception", http.StatusText(e.Status)).Outcome
	}
	writeJSON(w, e.Status, out)
}

// writeResponse áp dụng header Prefer cho create/update/patch (return=minimal | OperationOutcome).
func writeResponse(w http.ResponseWriter, req *Request, res *Response) {
	for k, v := range res.Header {
		w.Header().Set(k, v)
	}
	if res.Raw != nil {
		w.Header().Set("Content-Type", res.ContentType)
		w.WriteHeader(res.Status)
		_, _ = w.Write(res.Raw)
		return
	}
	if res.Resource == nil || res.Status == http.StatusNoContent {
		w.WriteHeader(res.Status)
		return
	}
	if req.Method == "POST" || req.Method == "PUT" || req.Method == "PATCH" {
		switch preferReturn(req.Header.Get("Prefer")) {
		case "minimal":
			w.WriteHeader(res.Status)
			return
		case "OperationOutcome":
			writeJSON(w, res.Status, &fhir.OperationOutcome{Issue: []fhir.OperationOutcomeIssue{{
				Severity: "information", Code: "informational", Details: &fhir.CodeableConcept{Text: "OK"},
			}}})
			return
		}
	}
	writeJSON(w, res.Status, res.Resource)
}

func preferReturn(h string) string {
	for _, part := range strings.Split(h, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		if strings.TrimSpace(k) == "return" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
