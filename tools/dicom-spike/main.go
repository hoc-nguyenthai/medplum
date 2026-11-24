package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// peak lấy mẫu HeapInuse để ước lượng đỉnh bộ nhớ.
func peak() (stop func() uint64) {
	var max atomic.Uint64
	done := make(chan struct{})
	go func() {
		var m runtime.MemStats
		for {
			select {
			case <-done:
				return
			case <-time.After(3 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapInuse > max.Load() {
					max.Store(m.HeapInuse)
				}
			}
		}
	}()
	return func() uint64 { close(done); return max.Load() }
}

func mb(b uint64) string { return fmt.Sprintf("%.1f MB", float64(b)/1e6) }

func describe(ds *dicom.Dataset) string {
	var tags []string
	for _, e := range ds.Elements {
		tags = append(tags, fmt.Sprintf("%v:%s", e.Tag, e.RawValueRepresentation))
	}
	return strings.Join(tags, " ")
}

func main() {
	mode, path := os.Args[1], os.Args[2]
	if mode == "multipart" {
		runMultipart()
		return
	}
	switch mode {
	case "json":
		runJSON(path)
		return
	case "stow":
		runStow(path)
		return
	case "multipart":
		runMultipart()
		return
	}
	if mode == "scan" {
		runScan(path)
		return
	}
	debug.SetGCPercent(100)
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	stop := peak()
	t0 := time.Now()
	var report string
	func() {
		defer func() {
			if r := recover(); r != nil {
				report = fmt.Sprintf("PANIC: %v", r)
			}
		}()
		switch mode {
		case "full": // Parse đầy đủ, mặc định
			ds, err := dicom.Parse(f, st.Size(), nil)
			report = summarize(&ds, err)
		case "skippx": // bỏ PixelData
			ds, err := dicom.Parse(f, st.Size(), nil, dicom.SkipPixelData())
			report = summarize(&ds, err)
		case "unproc": // đọc PixelData thô, không xử lý
			ds, err := dicom.Parse(f, st.Size(), nil, dicom.SkipProcessingPixelDataValue())
			report = summarize(&ds, err)
		case "head": // chỉ 100 KB đầu, như stow-rs.ts (untilOffset: 100*1024)
			lim := int64(100 * 1024)
			if st.Size() < lim {
				lim = st.Size()
			}
			p, err := dicom.NewParser(io.LimitReader(f, lim), lim, nil, dicom.SkipPixelData())
			if err != nil {
				report = "NewParser lỗi: " + err.Error()
				return
			}
			n := 0
			var last error
			for {
				_, e := p.Next()
				if e != nil {
					last = e
					break
				}
				n++
			}
			ds := p.GetMetadata()
			report = fmt.Sprintf("meta=%d phần tử, dataset %d phần tử, dừng vì: %v", len(ds.Elements), n, last)
		case "headstream": // đầu 100 KB từ luồng không tua (giống STOW), dừng khi gặp PixelData
			lim := int64(100 * 1024)
			p, err := dicom.NewParser(io.LimitReader(f, lim), lim, nil)
			if err != nil {
				report = "NewParser lỗi: " + err.Error()
				return
			}
			var got []string
			var last error
			for {
				e, err := p.Next()
				if err != nil {
					last = err
					break
				}
				got = append(got, e.Tag.String())
			}
			report = fmt.Sprintf("%d phần tử trước khi dừng (%v); cuối: %v", len(got), last, tail(got))
		}
	}()
	el := time.Since(t0)
	pk := stop()
	fmt.Printf("%-12s %-26s size=%s time=%v peak=%s\n    %s\n", mode, path[strings.LastIndex(path, "/")+1:], mb(uint64(st.Size())), el.Round(time.Millisecond), mb(pk), report)
}

func tail(s []string) []string {
	if len(s) > 3 {
		return s[len(s)-3:]
	}
	return s
}

func summarize(ds *dicom.Dataset, err error) string {
	var b strings.Builder
	if err != nil {
		fmt.Fprintf(&b, "LỖI: %v; ", err)
	}
	fmt.Fprintf(&b, "%d phần tử", len(ds.Elements))
	if ts, e := ds.FindElementByTag(tag.TransferSyntaxUID); e == nil {
		fmt.Fprintf(&b, ", TS=%v", ts.Value)
	}
	if pn, e := ds.FindElementByTag(tag.PatientName); e == nil {
		fmt.Fprintf(&b, ", PN=%v", pn.Value)
	}
	if px, e := ds.FindElementByTag(tag.PixelData); e == nil {
		if info, ok := px.Value.GetValue().(dicom.PixelDataInfo); ok {
			fmt.Fprintf(&b, ", PixelData: frames=%d encap=%v skipped=%v unprocessed=%v(%d B) parseErr=%v", len(info.Frames), info.IsEncapsulated, info.IntentionallySkipped, info.IntentionallyUnprocessed, len(info.UnprocessedValueData), info.ParseErr)
			for i, fr := range info.Frames {
				if i >= 3 {
					break
				}
				if fr.Encapsulated {
					fmt.Fprintf(&b, " [f%d enc %d B]", i, len(fr.EncapsulatedData.Data))
				} else {
					n, _ := fr.GetNativeFrame()
					fmt.Fprintf(&b, " [f%d native %dx%d]", i, n.Rows(), n.Cols())
				}
			}
		}
	}
	_ = errors.New
	_ = bytes.NewReader
	_ = sort.Strings
	return b.String()
}
