package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// toJSON: chuyển phần tử của thư viện sang DICOM JSON model (PS3.18 F.2), theo cleanDicomJsonDict của Medplum.
func toJSON(elems []*dicom.Element) map[string]any {
	out := map[string]any{}
	for _, e := range elems {
		if e.Tag.Group == 2 || e.Tag == tag.PixelData || (e.Tag.Group&0xFF00 == 0x6000 && e.Tag.Element == 0x3000) {
			continue
		}
		key := fmt.Sprintf("%04X%04X", e.Tag.Group, e.Tag.Element)
		vr := e.RawValueRepresentation
		m := map[string]any{"vr": vr}
		switch v := e.Value.GetValue().(type) {
		case []string:
			if len(v) == 0 {
				break
			}
			if vr == "PN" {
				var l []any
				for _, s := range v {
					l = append(l, map[string]any{"Alphabetic": s})
				}
				m["Value"] = l
			} else if vr == "DS" || vr == "IS" {
				var l []any
				for _, s := range v {
					if n, err := json.Number(strings.TrimSpace(s)).Float64(); err == nil {
						l = append(l, n)
					}
				}
				m["Value"] = l
			} else {
				m["Value"] = v
			}
		case []int:
			m["Value"] = v
		case []float64:
			m["Value"] = v
		case []byte:
			if len(v) > 10*1024 {
				continue // như Medplum: phần tử nhị phân lớn bị bỏ
			}
			m["InlineBinary"] = base64.StdEncoding.EncodeToString(v)
		case []*dicom.SequenceItemValue:
			var items []any
			for _, it := range v {
				items = append(items, toJSON(it.GetValue().([]*dicom.Element)))
			}
			m["Value"] = items
		default:
			m["_unsupported"] = fmt.Sprintf("%T", v)
		}
		out[key] = m
	}
	return out
}

func runJSON(path string) {
	f, _ := os.Open(path)
	defer f.Close()
	st, _ := f.Stat()
	ds, err := dicom.Parse(f, st.Size(), nil, dicom.SkipPixelData())
	if err != nil {
		fmt.Println("lỗi:", err)
	}
	j := toJSON(ds.Elements)
	b, _ := json.Marshal(j)
	fmt.Printf("json %s: %d khóa, %d byte\n", path[strings.LastIndex(path, "/")+1:], len(j), len(b))
	for _, k := range []string{"00100010", "00100030", "00080090", "00080060", "00280010", "00280008", "00201041", "00091001", "00081110", "00200013"} {
		if v, ok := j[k]; ok {
			s, _ := json.Marshal(v)
			if len(s) > 110 {
				s = append(s[:110], []byte("...")...)
			}
			fmt.Printf("  %s %s\n", k, s)
		} else {
			fmt.Printf("  %s (không có)\n", k)
		}
	}
}

// stow giả lập đường STOW: luồng multipart -> tee vào tệp (như kho) và vào bộ đọc metadata, đo bộ nhớ và thời gian.
func runStow(path string) {
	f, _ := os.Open(path)
	defer f.Close()
	st, _ := f.Stat()
	// dựng multipart/related bằng luồng (không nạp cả tệp)
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", "application/dicom")
		w, _ := mw.CreatePart(h)
		io.Copy(w, f)
		mw.Close()
		pw.Close()
	}()
	ct := mime.FormatMediaType("multipart/related", map[string]string{"type": "application/dicom", "boundary": mw.Boundary()})
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || mt != "multipart/related" {
		fmt.Println("ParseMediaType lỗi:", err, mt)
		return
	}
	stop := peak()
	t0 := time.Now()
	mr := multipart.NewReader(pr, params["boundary"])
	n := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("NextPart lỗi:", err)
			return
		}
		// tee: một nhánh ghi kho (tệp tạm), một nhánh cho bộ đọc metadata (đọc hết, bỏ PixelData)
		tmp, _ := os.CreateTemp("", "stow")
		mpr, mpw := io.Pipe()
		done := make(chan struct{})
		var meta *dicom.Dataset
		var perr error
		go func() {
			defer close(done)
			cb := &countingDiscard{}
			ds, err := dicom.ParseUntilEOF(mpr, nil, dicom.SkipPixelData())
			io.Copy(cb, mpr) // xả phần còn lại để không chặn đường ghi
			meta, perr = &ds, err
		}()
		w, _ := io.Copy(io.MultiWriter(tmp, mpw), part)
		mpw.Close()
		<-done
		tmp.Close()
		os.Remove(tmp.Name())
		n++
		fmt.Printf("stow part %d: %d byte (cho %d), metadata %d phần tử, lỗi parse: %v\n", n, w, st.Size(), len(meta.Elements), perr)
	}
	fmt.Printf("stow %s: %v, đỉnh bộ nhớ %s\n", path[strings.LastIndex(path, "/")+1:], time.Since(t0).Round(time.Millisecond), mb(stop()))
}

type countingDiscard struct{ n int64 }

func (c *countingDiscard) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// runMultipart: boundary có dấu ngoặc kép, type có ngoặc kép, phần không có Content-Type, và đáp WADO-RS (multipart/related có type và boundary).
func runMultipart() {
	body := "--ab c\r\nContent-Type: application/dicom\r\n\r\nAAAA\r\n--ab c\r\n\r\nBBBB\r\n--ab c--\r\n"
	for _, ct := range []string{`multipart/related; type="application/dicom"; boundary="ab c"`, `multipart/related; boundary=ab c`, `multipart/related; type=application/dicom; boundary=abc`, `application/dicom`} {
		mt, p, err := mime.ParseMediaType(ct)
		if err != nil {
			fmt.Printf("  %-70q lỗi: %v\n", ct, err)
			continue
		}
		var parts []string
		if strings.HasPrefix(mt, "multipart/") {
			mr := multipart.NewReader(strings.NewReader(body), p["boundary"])
			for {
				pt, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(pt)
				parts = append(parts, fmt.Sprintf("%s[%s]", b, pt.Header.Get("Content-Type")))
			}
		}
		fmt.Printf("  %-70q -> %s type=%q boundary=%q phần=%v\n", ct, mt, p["type"], p["boundary"], parts)
	}
	// đáp WADO-RS
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "image/jpeg")
	w, _ := mw.CreatePart(h)
	w.Write([]byte{0xff, 0xd8})
	mw.Close()
	fmt.Printf("  đáp: %s\n", mime.FormatMediaType("multipart/related", map[string]string{"type": "image/jpeg", "boundary": mw.Boundary()}))
}
