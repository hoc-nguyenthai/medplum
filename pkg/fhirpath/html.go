package fhirpath

import (
	"encoding/xml"
	"io"
	"strings"
)

// htmlOK triển khai htmlChecks() của FHIR (txt-1, txt-2): narrative phải là XHTML với gốc <div>
// trong namespace XHTML, chỉ dùng các phần tử định dạng cơ bản và không có thuộc tính xử lý sự kiện.
func htmlOK(s string) bool {
	dec := xml.NewDecoder(strings.NewReader(s))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return depth == 0
		}
		if err != nil {
			return false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 && (t.Name.Local != "div" || t.Name.Space != "http://www.w3.org/1999/xhtml") {
				return false
			}
			if !allowedHTML[strings.ToLower(t.Name.Local)] {
				return false
			}
			for _, a := range t.Attr {
				if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") {
					return false
				}
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

var allowedHTML = func() map[string]bool {
	m := map[string]bool{}
	for _, e := range strings.Fields(`a abbr acronym b big blockquote br caption cite code col colgroup dd dfn div dl dt em
		h1 h2 h3 h4 h5 h6 hr i img li ol p pre q samp small span strong sub sup table tbody td tfoot th thead tr tt u ul var`) {
		m[e] = true
	}
	return m
}()
