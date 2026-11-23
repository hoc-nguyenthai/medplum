package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trivita-fhir/apps/fhir-cli/internal/client"
)

type resourceLine struct {
	n   int // số thứ tự (dòng trong NDJSON hoặc vị trí trong Bundle), tính từ 1
	raw json.RawMessage
}

// parseResources đọc đầu vào là một Bundle (lấy entry[].resource), một resource đơn lẻ hoặc NDJSON.
func parseResources(data []byte) ([]resourceLine, error) {
	trim := bytes.TrimSpace(data)
	if len(trim) == 0 {
		return nil, errors.New("đầu vào rỗng")
	}
	var doc struct {
		ResourceType string `json:"resourceType"`
		Entry        []struct {
			Resource json.RawMessage `json:"resource"`
		} `json:"entry"`
	}
	if json.Valid(trim) { // một giá trị JSON duy nhất
		if err := json.Unmarshal(trim, &doc); err != nil || doc.ResourceType == "" {
			return nil, errors.New("đầu vào phải là resource hoặc Bundle có resourceType")
		}
		if doc.ResourceType != "Bundle" {
			return []resourceLine{{1, json.RawMessage(trim)}}, nil
		}
		var out []resourceLine
		for i, e := range doc.Entry {
			if len(e.Resource) == 0 {
				return nil, fmt.Errorf("entry %d của Bundle không có resource", i+1)
			}
			out = append(out, resourceLine{i + 1, e.Resource})
		}
		return out, nil
	}
	var out []resourceLine
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			return nil, fmt.Errorf("dòng %d không phải JSON hợp lệ", n)
		}
		out = append(out, resourceLine{n, append(json.RawMessage(nil), line...)})
	}
	return out, sc.Err()
}

// entryFor dựng entry batch/transaction: có id thì PUT Type/id, không thì POST Type.
func entryFor(r resourceLine) (map[string]any, string, error) {
	var h struct {
		ResourceType string `json:"resourceType"`
		ID           string `json:"id"`
	}
	if err := json.Unmarshal(r.raw, &h); err != nil || h.ResourceType == "" {
		return nil, "", fmt.Errorf("mục %d thiếu resourceType", r.n)
	}
	e := map[string]any{"resource": r.raw}
	label := h.ResourceType
	if h.ID != "" {
		e["request"] = map[string]any{"method": "PUT", "url": h.ResourceType + "/" + h.ID}
		label += "/" + h.ID
	} else {
		e["request"] = map[string]any{"method": "POST", "url": h.ResourceType}
	}
	return e, label, nil
}

func (a *app) importCmd(ctx context.Context, args []string) error {
	fs := a.flags("import")
	file := fs.String("f", "", "tệp NDJSON hoặc Bundle (hoặc - cho stdin)")
	mode := fs.String("mode", "batch", "batch (mục lỗi không ảnh hưởng mục khác) hoặc transaction (tất cả hoặc không)")
	size := fs.Int("size", 100, "số resource mỗi Bundle gửi đi")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 || (*mode != "batch" && *mode != "transaction") || *size < 1 {
		return usageError("cách dùng: import -f FILE|- [-mode batch|transaction] [-size N]")
	}
	data, err := a.readInput(*file)
	if err != nil {
		return err
	}
	items, err := parseResources(data)
	if err != nil {
		return err
	}
	ok, failed := 0, 0
	for start := 0; start < len(items); start += *size {
		end := min(start+*size, len(items))
		var entries []any
		var labels []string
		for _, it := range items[start:end] {
			e, label, err := entryFor(it)
			if err != nil {
				return err
			}
			entries = append(entries, e)
			labels = append(labels, label)
		}
		body, _ := json.Marshal(map[string]any{"resourceType": "Bundle", "type": *mode, "entry": entries})
		res, err := a.c.Do(ctx, "POST", "", body)
		if err != nil {
			return fmt.Errorf("gửi Bundle (mục %d-%d): %w", items[start].n, items[end-1].n, err)
		}
		var rb struct {
			Entry []struct {
				Response struct {
					Status  string `json:"status"`
					Outcome struct {
						Issue []struct {
							Diagnostics string `json:"diagnostics"`
						} `json:"issue"`
					} `json:"outcome"`
				} `json:"response"`
			} `json:"entry"`
		}
		if err := json.Unmarshal(res.Body, &rb); err != nil || len(rb.Entry) != len(entries) {
			return fmt.Errorf("phản hồi Bundle không khớp số mục gửi (%d)", len(entries))
		}
		for i, e := range rb.Entry {
			if strings.HasPrefix(e.Response.Status, "2") {
				ok++
				continue
			}
			failed++
			msg := e.Response.Status
			if len(e.Response.Outcome.Issue) > 0 && e.Response.Outcome.Issue[0].Diagnostics != "" {
				msg += ": " + e.Response.Outcome.Issue[0].Diagnostics
			}
			fmt.Fprintf(a.env.Stderr, "mục %d (%s): %s\n", items[start+i].n, labels[i], msg)
		}
	}
	fmt.Fprintf(a.env.Stdout, "đã nạp %d, lỗi %d, tổng %d\n", ok, failed, len(items))
	if failed > 0 {
		return fmt.Errorf("%d resource không nạp được", failed)
	}
	return nil
}

// wait hỏi trạng thái một job nền (link do máy chủ trả về) tới khi xong; 202 là còn chạy.
func (a *app) wait(ctx context.Context, statusRef string, limit time.Duration) (*client.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		res, err := a.c.Do(ctx, "GET", statusRef, nil)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("hết thời gian chờ job (%s)", limit)
			}
			return nil, err
		}
		if res.Status != 202 {
			return res, nil
		}
		select {
		case <-time.After(a.env.PollInterval):
		case <-ctx.Done():
			return nil, fmt.Errorf("hết thời gian chờ job (%s)", limit)
		}
	}
}

func (a *app) export(ctx context.Context, args []string) error {
	fs := a.flags("export")
	out := fs.String("o", "", "thư mục lưu file ndjson (bắt buộc)")
	level := fs.String("level", "system", "system, patient hoặc group/ID")
	types := fs.String("type", "", "chỉ xuất các loại này (cách nhau bằng dấu phẩy)")
	since := fs.String("since", "", "chỉ xuất resource cập nhật sau thời điểm này (RFC3339)")
	var filters []string
	fs.Func("filter", "điều kiện lọc theo loại dạng Type?query (_typeFilter), có thể lặp lại", func(v string) error { filters = append(filters, v); return nil })
	keep := fs.Bool("keep", false, "giữ job và file trên máy chủ sau khi tải")
	limit := fs.Duration("wait", 30*time.Minute, "thời gian chờ tối đa")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 || *out == "" {
		return usageError("cách dùng: export -o DIR [-level system|patient|group/ID] [-type A,B] [-filter Type?query]... [-since T] [-keep]")
	}
	var path string
	switch {
	case *level == "system":
		path = "$export"
	case *level == "patient":
		path = "Patient/$export"
	case strings.HasPrefix(*level, "group/") && len(*level) > len("group/"):
		path = "Group/" + strings.TrimPrefix(*level, "group/") + "/$export"
	default:
		return usageError("-level phải là system, patient hoặc group/ID")
	}
	q := url.Values{}
	if *types != "" {
		q.Set("_type", *types)
	}
	if *since != "" {
		q.Set("_since", *since)
	}
	for _, f := range filters {
		q.Add("_typeFilter", f)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	acc, err := a.c.Do(ctx, "GET", path, nil, "Prefer", "respond-async")
	if err != nil {
		return err
	}
	status := acc.Header.Get("Content-Location")
	if acc.Status != 202 || status == "" {
		return fmt.Errorf("máy chủ không bắt đầu job export (HTTP %d)", acc.Status)
	}
	fmt.Fprintln(a.env.Stderr, "đã tạo job export, đang chờ...")
	res, err := a.wait(ctx, status, *limit)
	if err != nil {
		return err
	}
	var manifest struct {
		Output []struct {
			Type  string `json:"type"`
			URL   string `json:"url"`
			Count int    `json:"count"`
		} `json:"output"`
	}
	if err := json.Unmarshal(res.Body, &manifest); err != nil {
		return fmt.Errorf("manifest không hợp lệ: %w", err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	perType := map[string]int{}
	total := 0
	for _, f := range manifest.Output {
		if f.Type == "" || strings.ContainsAny(f.Type, `/\`) || strings.Contains(f.Type, "..") {
			return fmt.Errorf("manifest có tên loại không hợp lệ: %q", f.Type)
		}
		file, err := a.c.Do(ctx, "GET", f.URL, nil, "Accept", "application/fhir+ndjson")
		if err != nil {
			return fmt.Errorf("tải %s: %w", f.URL, err)
		}
		perType[f.Type]++
		name := fmt.Sprintf("%s-%d.ndjson", f.Type, perType[f.Type])
		if err := os.WriteFile(filepath.Join(*out, name), file.Body, 0o644); err != nil {
			return err
		}
		total += f.Count
		fmt.Fprintf(a.env.Stdout, "%s\t%d resource\n", filepath.Join(*out, name), f.Count)
	}
	fmt.Fprintf(a.env.Stderr, "xong: %d file, %d resource\n", len(manifest.Output), total)
	if !*keep {
		if _, err := a.c.Do(ctx, "DELETE", status, nil); err != nil {
			fmt.Fprintln(a.env.Stderr, "cảnh báo: không dọn được job trên máy chủ:", err)
		}
	}
	return nil
}

func (a *app) reindex(ctx context.Context, args []string) error {
	fs := a.flags("reindex")
	limit := fs.Duration("wait", 30*time.Minute, "thời gian chờ tối đa")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 || strings.Contains(pos[0], "/") {
		return usageError("cách dùng: reindex Type [id]")
	}
	path := pos[0]
	if len(pos) == 2 {
		path += "/" + pos[1]
	}
	acc, err := a.c.Do(ctx, "POST", path+"/$reindex", []byte(""))
	if err != nil {
		return err
	}
	status := acc.Header.Get("Content-Location")
	if status == "" {
		return a.printJSON(acc.Body) // chạy đồng bộ
	}
	res, err := a.wait(ctx, status, *limit)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.env.Stderr, "reindex xong")
	return a.printJSON(res.Body)
}
