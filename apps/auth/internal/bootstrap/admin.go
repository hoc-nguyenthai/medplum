package bootstrap

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"trivita-fhir/apps/auth/internal/application"
	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/fhir"
)

// AdminUsage là cách dùng các lệnh quản trị.
const AdminUsage = `lệnh quản trị:
  auth seed project -name <tên> -admin-email <email> -admin-first <tên> -admin-last <họ>
        tạo project (kèm access policy mặc định, client mặc định) và admin đầu tiên; mật khẩu admin (chỉ cần khi admin chưa có)
        đọc từ biến môi trường AUTH_SEED_ADMIN_PASSWORD hoặc từ dòng đầu của stdin, KHÔNG nhận qua tham số
  auth client create -project <id> -name <tên> [-description <mô tả>] [-redirect-uri <uri>]... [-grant-type <g>]...
        [-pkce-optional] [-access-policy AccessPolicy/<id>]
        tạo client OAuth; secret chỉ hiện MỘT lần
  auth user set-password -email <email> [-project <id>]
        đặt lại mật khẩu (AUTH_NEW_PASSWORD hoặc stdin) và thu hồi mọi login của user
  auth domain add -domain <tên miền> -authorize-url <url> -token-url <url> -client-id <id> [-user-info-url <url>] [-jwks-url <url> -issuer <iss>]
        [-audience <aud>] [-client-secret-post] [-use-pkce] [-use-subject] [-allowed-redirect-url <url>]...
        cấu hình identity provider ngoài cho một tên miền email (đăng nhập ngoài, P6). URL phải là https và không trỏ vào địa chỉ nội bộ, và phải có -jwks-url
        (id_token được xác minh chữ ký) trừ khi AUTH_ALLOW_PRIVATE_JWKS=true (dev/test). Secret của IdP đọc từ AUTH_DOMAIN_IDP_CLIENT_SECRET hoặc dòng đầu của stdin
  auth project feature -project <id> -feature <tên> [-disable]
        bật (mặc định) hoặc tắt một tính năng của project, ví dụ "scim" (SCIM 2.0 cần cả AUTH_SCIM_ENABLED và tính năng này)
  auth purge-logins -before <RFC3339|YYYY-MM-DD> (-dry-run | -yes)
        xóa VĨNH VIỄN các login không cập nhật từ trước mốc đó (cắt cả refresh token của chúng)`

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// IsAdminCommand cho biết args[0] là lệnh quản trị.
func IsAdminCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "seed", "client", "user", "purge-logins", "domain", "project":
		return true
	}
	return false
}

// RunAdmin chạy một lệnh quản trị trên database cấu hình. getenv và in được tiêm để test; mật khẩu không bao giờ lấy từ argv.
func RunAdmin(ctx context.Context, s config.Settings, args []string, getenv func(string) string, in io.Reader, out io.Writer) error {
	c, err := New(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = c.Stop(context.Background()) }()
	svc := c.Auth

	switch {
	case len(args) >= 2 && args[0] == "seed" && args[1] == "project":
		return seedProject(ctx, s, svc, args[2:], getenv, in, out)
	case len(args) >= 2 && args[0] == "client" && args[1] == "create":
		return createClient(ctx, svc, args[2:], out)
	case len(args) >= 2 && args[0] == "user" && args[1] == "set-password":
		return setUserPassword(ctx, svc, args[2:], getenv, in, out)
	case len(args) >= 2 && args[0] == "domain" && args[1] == "add":
		return addDomain(ctx, s, svc, args[2:], getenv, in, out)
	case len(args) >= 2 && args[0] == "project" && args[1] == "feature":
		return projectFeature(ctx, svc, args[2:], out)
	case len(args) >= 1 && args[0] == "purge-logins":
		return purgeLogins(ctx, svc, args[1:], out)
	}
	return errors.New(AdminUsage)
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// secretFrom đọc bí mật từ biến môi trường, nếu không có thì từ dòng đầu của stdin.
func secretFrom(getenv func(string) string, key string, in io.Reader) (string, error) {
	if v := getenv(key); v != "" {
		return v, nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", fmt.Errorf("thiếu mật khẩu: đặt %s hoặc đưa vào stdin", key)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", fmt.Errorf("thiếu mật khẩu: đặt %s hoặc đưa vào stdin", key)
	}
	return line, nil
}

func seedProject(ctx context.Context, s config.Settings, svc *application.Service, args []string, getenv func(string) string, in io.Reader, out io.Writer) error {
	fs := newFlags("seed project")
	name := fs.String("name", "", "tên project (bắt buộc)")
	email := fs.String("admin-email", "", "email admin (bắt buộc)")
	first := fs.String("admin-first", "", "tên admin")
	last := fs.String("admin-last", "", "họ admin")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, AdminUsage)
	}
	if *name == "" || *email == "" {
		return fmt.Errorf("thiếu -name hoặc -admin-email\n%s", AdminUsage)
	}
	// Chỉ đọc mật khẩu khi admin CHƯA có (admin đã có thì giữ nguyên mật khẩu).
	existing, err := svc.FindUserInScope(ctx, *email, "")
	if err != nil {
		return err
	}
	pw := ""
	if existing == nil {
		if pw, err = secretFrom(getenv, "AUTH_SEED_ADMIN_PASSWORD", in); err != nil {
			return err
		}
	}
	res, err := svc.SeedProject(ctx, application.SeedProjectRequest{
		Name: *name, AdminEmail: *email, AdminFirst: *first, AdminLast: *last, AdminPassword: pw, Features: splitList(s.DefaultProjectFeatures),
	})
	if err != nil {
		return explain(err)
	}
	fmt.Fprintf(out, "projectId: %s\nprojectName: %s\nadminEmail: %s\nadminReused: %t\nclientId: %s\nclientSecret: %s\n",
		res.Project.ID, res.Project.Name, strings.ToLower(*email), existing != nil, res.Client.ID, res.Client.Secret)
	fmt.Fprintln(out, "# clientSecret chỉ hiện một lần: lưu ngay vào nơi an toàn.")
	return nil
}

func createClient(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := newFlags("client create")
	project := fs.String("project", "", "id project (bắt buộc)")
	name := fs.String("name", "", "tên client (bắt buộc)")
	desc := fs.String("description", "", "mô tả")
	pkce := fs.Bool("pkce-optional", false, "cho phép bỏ PKCE (client công khai cũ); mặc định PKCE bắt buộc")
	policy := fs.String("access-policy", "", "AccessPolicy/<id> của membership client (mặc định: policy ClientApplication của project)")
	var redirects, grants stringList
	fs.Var(&redirects, "redirect-uri", "redirect URI (lặp được)")
	fs.Var(&grants, "grant-type", "grant type (lặp được), ví dụ refresh_token")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, AdminUsage)
	}
	if *project == "" || *name == "" {
		return fmt.Errorf("thiếu -project hoặc -name\n%s", AdminUsage)
	}
	req := application.NewClient{Name: *name, Description: *desc, RedirectURIs: redirects, GrantTypes: grants, PkceOptional: *pkce}
	if *policy != "" {
		if !strings.HasPrefix(*policy, "AccessPolicy/") {
			return errors.New("-access-policy phải có dạng AccessPolicy/<id>")
		}
		req.AccessPolicy = &fhir.Reference{Reference: *policy}
	}
	client, err := svc.CreateClient(ctx, *project, req)
	if err != nil {
		return explain(err)
	}
	fmt.Fprintf(out, "clientId: %s\nclientSecret: %s\n", client.ID, client.Secret)
	fmt.Fprintln(out, "# clientSecret chỉ hiện một lần: lưu ngay vào nơi an toàn.")
	return nil
}

func setUserPassword(ctx context.Context, svc *application.Service, args []string, getenv func(string) string, in io.Reader, out io.Writer) error {
	fs := newFlags("user set-password")
	email := fs.String("email", "", "email user (bắt buộc)")
	project := fs.String("project", "", "id project của user (rỗng: user không gắn project)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, AdminUsage)
	}
	if *email == "" {
		return fmt.Errorf("thiếu -email\n%s", AdminUsage)
	}
	pw, err := secretFrom(getenv, "AUTH_NEW_PASSWORD", in)
	if err != nil {
		return err
	}
	if err := svc.SetUserPassword(ctx, *email, *project, pw); err != nil {
		return explain(err)
	}
	fmt.Fprintln(out, "đã đặt mật khẩu và thu hồi mọi login của user")
	return nil
}

func purgeLogins(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := newFlags("purge-logins")
	before := fs.String("before", "", "mốc thời gian (RFC3339 hoặc YYYY-MM-DD)")
	dry := fs.Bool("dry-run", false, "chỉ đếm, không xóa")
	yes := fs.Bool("yes", false, "xác nhận xóa vĩnh viễn")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, AdminUsage)
	}
	if *before == "" {
		return fmt.Errorf("thiếu -before\n%s", AdminUsage)
	}
	if !*dry && !*yes {
		return errors.New("xóa vĩnh viễn không hoàn tác được: thêm -dry-run để đếm trước, hoặc -yes để xác nhận xóa")
	}
	t, err := parseTime(*before)
	if err != nil {
		return err
	}
	n, err := svc.PurgeLogins(ctx, t, *dry)
	if err != nil {
		return explain(err)
	}
	if *dry {
		fmt.Fprintf(out, "sẽ xóa %d login (dry-run, chưa xóa gì)\n", n)
	} else {
		fmt.Fprintf(out, "đã xóa vĩnh viễn %d login\n", n)
	}
	return nil
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("-before %q không phải RFC3339 hay YYYY-MM-DD", s)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// explain trả thông điệp lỗi cho người vận hành: lỗi nghiệp vụ giữ nguyên chữ; lỗi nội bộ giữ chi tiết (CLI chạy bởi người
// vận hành tin cậy, khác với phản hồi HTTP).
func explain(err error) error {
	var de *domain.Error
	if errors.As(err, &de) && de.Expression != "" {
		return fmt.Errorf("%s (trường %s)", de.Message, de.Expression)
	}
	return err
}

func addDomain(ctx context.Context, s config.Settings, svc *application.Service, args []string, getenv func(string) string, in io.Reader, out io.Writer) error {
	fs := newFlags("domain add")
	dom := fs.String("domain", "", "tên miền email (bắt buộc)")
	authorize := fs.String("authorize-url", "", "authorizeUrl của IdP (bắt buộc)")
	token := fs.String("token-url", "", "tokenUrl của IdP (bắt buộc)")
	userInfo := fs.String("user-info-url", "", "userInfoUrl của IdP")
	jwks := fs.String("jwks-url", "", "jwksUrl của IdP (bắt buộc trừ dev/test)")
	issuer := fs.String("issuer", "", "issuer của id_token (bắt buộc khi có -jwks-url)")
	audience := fs.String("audience", "", "audience của id_token (mặc định client id)")
	clientID := fs.String("client-id", "", "client id đã đăng ký ở IdP (bắt buộc)")
	post := fs.Bool("client-secret-post", false, "gửi client secret trong body (mặc định HTTP Basic)")
	pkce := fs.Bool("use-pkce", false, "gửi code_verifier cho IdP")
	subject := fs.Bool("use-subject", false, "ánh xạ danh tính theo sub (ProjectMembership.externalId) thay vì email")
	var redirects stringList
	fs.Var(&redirects, "allowed-redirect-url", "URL được phép làm returnTo sau đăng nhập (lặp được)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, AdminUsage)
	}
	if *dom == "" || *authorize == "" || *token == "" || *clientID == "" {
		return fmt.Errorf("thiếu -domain, -authorize-url, -token-url hoặc -client-id\n%s", AdminUsage)
	}
	secret, err := secretFrom(getenv, "AUTH_DOMAIN_IDP_CLIENT_SECRET", in)
	if err != nil {
		return fmt.Errorf("thiếu client secret của IdP: đặt AUTH_DOMAIN_IDP_CLIENT_SECRET hoặc đưa vào stdin")
	}
	cfg, err := svc.AddDomainConfiguration(ctx, application.AddDomainRequest{
		Domain: *dom, AuthorizeURL: *authorize, TokenURL: *token, UserInfoURL: *userInfo, JWKSURL: *jwks, Issuer: *issuer, Audience: *audience,
		ClientID: *clientID, ClientSecret: secret, ClientSecretPost: *post, UsePKCE: *pkce, UseSubject: *subject, AllowedPostLoginRedirectURLs: redirects,
		AllowUnsafeURLs: s.AllowPrivateJWKS,
	})
	if err != nil {
		return explain(err)
	}
	fmt.Fprintf(out, "domainConfigurationId: %s\ndomain: %s\n", cfg.ID, cfg.Domain)
	return nil
}

func projectFeature(ctx context.Context, svc *application.Service, args []string, out io.Writer) error {
	fs := newFlags("project feature")
	project := fs.String("project", "", "id project (bắt buộc)")
	feature := fs.String("feature", "", "tên tính năng (bắt buộc)")
	disable := fs.Bool("disable", false, "tắt thay vì bật")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, AdminUsage)
	}
	if *project == "" || *feature == "" {
		return fmt.Errorf("thiếu -project hoặc -feature\n%s", AdminUsage)
	}
	if err := svc.SetProjectFeature(ctx, *project, *feature, !*disable); err != nil {
		return explain(err)
	}
	state := "enabled"
	if *disable {
		state = "disabled"
	}
	fmt.Fprintf(out, "project: %s\nfeature: %s\nstate: %s\n", *project, *feature, state)
	return nil
}
