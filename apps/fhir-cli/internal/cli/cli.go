// Package cli là các lệnh của fhir-cli: đọc, tìm kiếm, ghi, xóa, import và export hàng loạt, reindex.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"trivita-fhir/apps/fhir-cli/internal/client"
)

const usage = `fhir-cli - công cụ dòng lệnh cho fhir-server

Cách dùng: fhir-cli [tùy chọn chung] <lệnh> [tham số]

Tùy chọn chung (có thể đặt bằng biến môi trường):
  -base URL      gốc API, mặc định $FHIR_BASE_URL hoặc http://localhost:8005/fhir/R4
  -token TOKEN   bearer token, mặc định $FHIR_TOKEN
  -project ID    project (header X-Fhir-Project, dành cho super-admin), mặc định $FHIR_PROJECT
  -timeout D     timeout mỗi yêu cầu HTTP, mặc định 60s

Lệnh:
  read   Type/id [-version V]             đọc một resource
  search Type [query] [-all] [-ndjson]    tìm kiếm (query dạng a=b&c=d); -all đi hết các trang
  create Type -f FILE|-                   POST một resource
  update Type/id -f FILE|-                PUT một resource
  delete Type/id                          xóa một resource
  import -f FILE|- [-mode batch|transaction] [-size N]
                                          nạp NDJSON (mỗi dòng một resource) hoặc một Bundle; trả mã lỗi nếu có mục thất bại
  export -o DIR [-level system|patient|group/ID] [-type A,B] [-filter Type?query]... [-since T] [-keep]
                                          bulk export, chờ xong rồi tải file ndjson về DIR
  reindex Type [id]                       lập lại chỉ mục (cần quyền admin), chờ job xong
  help                                    hiện trợ giúp
`

// Env là môi trường chạy (để kiểm thử).
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// PollInterval là khoảng chờ giữa các lần hỏi trạng thái job (mặc định 1s).
	PollInterval time.Duration
}

// Run chạy một lệnh và trả mã thoát (0 thành công, 1 lỗi, 2 sai cách dùng).
func Run(ctx context.Context, args []string, env Env) int {
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if env.PollInterval <= 0 {
		env.PollInterval = time.Second
	}
	fs := flag.NewFlagSet("fhir-cli", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { fmt.Fprint(env.Stderr, usage) }
	base := fs.String("base", firstNonEmpty(env.Getenv("FHIR_BASE_URL"), "http://localhost:8005/fhir/R4"), "")
	token := fs.String("token", env.Getenv("FHIR_TOKEN"), "")
	project := fs.String("project", env.Getenv("FHIR_PROJECT"), "")
	timeout := fs.Duration("timeout", 60*time.Second, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 || rest[0] == "help" {
		fmt.Fprint(env.Stderr, usage)
		if len(rest) == 0 {
			return 2
		}
		return 0
	}
	cl, err := client.New(*base, *token, *project, *timeout)
	if err != nil {
		fmt.Fprintln(env.Stderr, "lỗi:", err)
		return 2
	}
	app := &app{c: cl, env: env}
	cmd, cargs := rest[0], rest[1:]
	var run func(context.Context, []string) error
	switch cmd {
	case "read":
		run = app.read
	case "search":
		run = app.search
	case "create":
		run = app.write("POST")
	case "update":
		run = app.write("PUT")
	case "delete":
		run = app.delete
	case "import":
		run = app.importCmd
	case "export":
		run = app.export
	case "reindex":
		run = app.reindex
	default:
		fmt.Fprintf(env.Stderr, "lệnh không biết: %q\n\n%s", cmd, usage)
		return 2
	}
	if err := run(ctx, cargs); err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintln(env.Stderr, "lỗi:", ue.Error())
			return 2
		}
		fmt.Fprintln(env.Stderr, "lỗi:", err)
		return 1
	}
	return 0
}

type usageError string

func (e usageError) Error() string { return string(e) }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type app struct {
	c   *client.Client
	env Env
}

// flags dựng FlagSet con cho một lệnh; parse chấp nhận tham số vị trí đứng trước hoặc sau cờ.
func (a *app) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.env.Stderr)
	return fs
}

// parse tách các tham số vị trí khỏi cờ (cho phép "read Patient/1 -version 2" và "read -version 2 Patient/1").
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, usageError(err.Error())
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, nil
}

func (a *app) printJSON(raw []byte) error {
	var buf strings.Builder
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		_, err = a.env.Stdout.Write(raw)
		return err
	}
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := io.WriteString(a.env.Stdout, buf.String())
	return err
}

func splitRef(ref string) (rt, id string, err error) {
	rt, id, ok := strings.Cut(ref, "/")
	if !ok || rt == "" || id == "" || strings.Contains(id, "/") {
		return "", "", usageError(fmt.Sprintf("cần dạng Type/id, nhận %q", ref))
	}
	return rt, id, nil
}

func (a *app) readInput(file string) ([]byte, error) {
	if file == "" {
		return nil, usageError("cần -f FILE (hoặc -f - để đọc stdin)")
	}
	if file == "-" {
		return io.ReadAll(a.env.Stdin)
	}
	return os.ReadFile(file)
}

func (a *app) read(ctx context.Context, args []string) error {
	fs := a.flags("read")
	version := fs.String("version", "", "đọc một phiên bản cụ thể")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageError("cách dùng: read Type/id [-version V]")
	}
	rt, id, err := splitRef(pos[0])
	if err != nil {
		return err
	}
	path := rt + "/" + id
	if *version != "" {
		path += "/_history/" + *version
	}
	res, err := a.c.Do(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	return a.printJSON(res.Body)
}

func (a *app) write(method string) func(context.Context, []string) error {
	return func(ctx context.Context, args []string) error {
		fs := a.flags(strings.ToLower(method))
		file := fs.String("f", "", "tệp JSON (hoặc - cho stdin)")
		pos, err := parse(fs, args)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageError("cách dùng: create Type -f FILE | update Type/id -f FILE")
		}
		path := pos[0]
		if method == "PUT" {
			if _, _, err := splitRef(path); err != nil {
				return err
			}
		} else if strings.Contains(path, "/") {
			return usageError("create nhận tên loại resource, ví dụ create Patient -f p.json")
		}
		body, err := a.readInput(*file)
		if err != nil {
			return err
		}
		if !json.Valid(body) {
			return errors.New("nội dung không phải JSON hợp lệ")
		}
		res, err := a.c.Do(ctx, method, path, body)
		if err != nil {
			return err
		}
		if loc := res.Header.Get("Location"); loc != "" {
			fmt.Fprintln(a.env.Stderr, "Location:", loc)
		}
		return a.printJSON(res.Body)
	}
}

func (a *app) delete(ctx context.Context, args []string) error {
	fs := a.flags("delete")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageError("cách dùng: delete Type/id")
	}
	if _, _, err := splitRef(pos[0]); err != nil {
		return err
	}
	if _, err := a.c.Do(ctx, "DELETE", pos[0], nil); err != nil {
		return err
	}
	fmt.Fprintln(a.env.Stderr, "đã xóa", pos[0])
	return nil
}

func (a *app) search(ctx context.Context, args []string) error {
	fs := a.flags("search")
	all := fs.Bool("all", false, "đi hết mọi trang")
	nd := fs.Bool("ndjson", false, "in mỗi resource khớp một dòng JSON (bỏ resource include)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usageError("cách dùng: search Type [query] [-all] [-ndjson]")
	}
	path := pos[0]
	if len(pos) == 2 {
		path += "?" + strings.TrimPrefix(pos[1], "?")
	}
	enc := json.NewEncoder(a.env.Stdout)
	enc.SetEscapeHTML(false)
	for page := 0; ; page++ {
		res, err := a.c.Do(ctx, "GET", path, nil)
		if err != nil {
			return err
		}
		var b struct {
			Entry []struct {
				Resource json.RawMessage `json:"resource"`
				Search   struct {
					Mode string `json:"mode"`
				} `json:"search"`
			} `json:"entry"`
			Link []struct{ Relation, URL string } `json:"link"`
		}
		if err := json.Unmarshal(res.Body, &b); err != nil {
			return fmt.Errorf("phản hồi không phải Bundle: %w", err)
		}
		if !*nd {
			if *all {
				return usageError("-all cần -ndjson (mỗi trang là một Bundle riêng)")
			}
			return a.printJSON(res.Body)
		}
		for _, e := range b.Entry {
			if e.Search.Mode == "include" || len(e.Resource) == 0 {
				continue
			}
			if err := enc.Encode(e.Resource); err != nil {
				return err
			}
		}
		next := ""
		for _, l := range b.Link {
			if l.Relation == "next" {
				next = l.URL
			}
		}
		if !*all || next == "" {
			return nil
		}
		path = next
	}
}
