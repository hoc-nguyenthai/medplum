package fhirrouter

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// applyJSONPatch áp dụng RFC 6902 lên một cây JSON (map[string]any, []any, giá trị nguyên thủy).
func applyJSONPatch(doc any, ops []any) (any, error) {
	doc = deepCopy(doc)
	for i, raw := range ops {
		op, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("thao tác %d không phải object", i)
		}
		var err error
		if doc, err = applyOp(doc, op); err != nil {
			return nil, fmt.Errorf("thao tác %d (%v %v): %w", i, op["op"], op["path"], err)
		}
	}
	return doc, nil
}

func applyOp(doc any, op map[string]any) (any, error) {
	kind, _ := op["op"].(string)
	path, ok := op["path"].(string)
	if !ok {
		return nil, fmt.Errorf("thiếu path")
	}
	toks, err := parsePointer(path)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "add":
		v, ok := op["value"]
		if !ok {
			return nil, fmt.Errorf("thiếu value")
		}
		return addAt(doc, toks, deepCopy(v))
	case "remove":
		return removeAt(doc, toks)
	case "replace":
		v, ok := op["value"]
		if !ok {
			return nil, fmt.Errorf("thiếu value")
		}
		if _, err := getAt(doc, toks); err != nil {
			return nil, err
		}
		doc, err = removeAt(doc, toks)
		if err != nil {
			return nil, err
		}
		return addAt(doc, toks, deepCopy(v))
	case "move", "copy":
		from, _ := op["from"].(string)
		ftoks, err := parsePointer(from)
		if err != nil {
			return nil, err
		}
		v, err := getAt(doc, ftoks)
		if err != nil {
			return nil, err
		}
		v = deepCopy(v)
		if kind == "move" {
			if doc, err = removeAt(doc, ftoks); err != nil {
				return nil, err
			}
		}
		return addAt(doc, toks, v)
	case "test":
		cur, err := getAt(doc, toks)
		if err != nil {
			return nil, err
		}
		a, _ := json.Marshal(cur)
		b, _ := json.Marshal(op["value"])
		if string(a) != string(b) {
			return nil, fmt.Errorf("test thất bại")
		}
		return doc, nil
	}
	return nil, fmt.Errorf("op không hỗ trợ: %q", kind)
}

func parsePointer(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("JSON Pointer không hợp lệ: %q", p)
	}
	parts := strings.Split(p[1:], "/")
	for i, s := range parts {
		parts[i] = strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
	}
	return parts, nil
}

func index(tok string, n int, allowEnd bool) (int, error) {
	if allowEnd && tok == "-" {
		return n, nil
	}
	i, err := strconv.Atoi(tok)
	if err != nil || i < 0 || i > n || (!allowEnd && i == n) || (len(tok) > 1 && tok[0] == '0') {
		return 0, fmt.Errorf("chỉ số mảng không hợp lệ: %q", tok)
	}
	return i, nil
}

func getAt(cur any, toks []string) (any, error) {
	for _, t := range toks {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[t]
			if !ok {
				return nil, fmt.Errorf("không có khóa %q", t)
			}
			cur = v
		case []any:
			i, err := index(t, len(c), false)
			if err != nil {
				return nil, err
			}
			cur = c[i]
		default:
			return nil, fmt.Errorf("không đi tiếp được tại %q", t)
		}
	}
	return cur, nil
}

func addAt(cur any, toks []string, v any) (any, error) {
	if len(toks) == 0 {
		return v, nil
	}
	switch c := cur.(type) {
	case map[string]any:
		if len(toks) == 1 {
			c[toks[0]] = v
			return c, nil
		}
		child, ok := c[toks[0]]
		if !ok {
			return nil, fmt.Errorf("không có khóa %q", toks[0])
		}
		nc, err := addAt(child, toks[1:], v)
		if err != nil {
			return nil, err
		}
		c[toks[0]] = nc
		return c, nil
	case []any:
		if len(toks) == 1 {
			i, err := index(toks[0], len(c), true)
			if err != nil {
				return nil, err
			}
			c = append(c, nil)
			copy(c[i+1:], c[i:])
			c[i] = v
			return c, nil
		}
		i, err := index(toks[0], len(c), false)
		if err != nil {
			return nil, err
		}
		nc, err := addAt(c[i], toks[1:], v)
		if err != nil {
			return nil, err
		}
		c[i] = nc
		return c, nil
	}
	return nil, fmt.Errorf("không thêm được vào giá trị nguyên thủy")
}

func removeAt(cur any, toks []string) (any, error) {
	if len(toks) == 0 {
		return nil, fmt.Errorf("không xóa được gốc của tài liệu")
	}
	switch c := cur.(type) {
	case map[string]any:
		if len(toks) == 1 {
			if _, ok := c[toks[0]]; !ok {
				return nil, fmt.Errorf("không có khóa %q", toks[0])
			}
			delete(c, toks[0])
			return c, nil
		}
		child, ok := c[toks[0]]
		if !ok {
			return nil, fmt.Errorf("không có khóa %q", toks[0])
		}
		nc, err := removeAt(child, toks[1:])
		if err != nil {
			return nil, err
		}
		c[toks[0]] = nc
		return c, nil
	case []any:
		i, err := index(toks[0], len(c), false)
		if err != nil {
			return nil, err
		}
		if len(toks) == 1 {
			return append(c[:i], c[i+1:]...), nil
		}
		nc, err := removeAt(c[i], toks[1:])
		if err != nil {
			return nil, err
		}
		c[i] = nc
		return c, nil
	}
	return nil, fmt.Errorf("không xóa được trong giá trị nguyên thủy")
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		s := make([]any, len(x))
		for i, e := range x {
			s[i] = deepCopy(e)
		}
		return s
	}
	return v
}
