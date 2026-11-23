package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/apps/auth/internal/domain"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhir"
	"trivita-fhir/pkg/fhirsearch"
	"trivita-fhir/pkg/fhirstore"
)

func newContainer(t *testing.T, mut func(*config.Settings)) *Container {
	t.Helper()
	c, err := New(context.Background(), settings(t, mut))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	return c
}

func TestAuthServiceWiring(t *testing.T) {
	ctx := context.Background()
	c := newContainer(t, func(s *config.Settings) {
		s.PwnedEnabled = false
		s.DefaultOAuthClients = `[{"id":"mcp","name":"MCP","redirectUris":["https://x/cb"]}]`
	})
	if c.Auth == nil {
		t.Fatal("Auth phải được lắp")
	}
	if cli, err := c.Auth.GetClientApplication(ctx, domain.CLIClientID); err != nil || cli.ID != domain.CLIClientID {
		t.Errorf("client CLI mặc định bật: %v %v", cli, err)
	}
	if mcp, err := c.Auth.GetClientApplication(ctx, "mcp"); err != nil || mcp.Name != "MCP" || mcp.ResourceType != "ClientApplication" {
		t.Errorf("client cấu hình thêm: %v %v", mcp, err)
	}
	if err := c.Auth.CheckPwned(ctx, "password123"); err != nil {
		t.Errorf("tắt pwned thì không kiểm: %v", err)
	}
	if h, err := c.Auth.HashPassword("mat-khau-du-dai"); err != nil || !c.Auth.VerifyPassword(&fhir.User{PasswordHash: h}, "mat-khau-du-dai") {
		t.Errorf("hasher: %v", err)
	}

	off := newContainer(t, func(s *config.Settings) { s.CLIClientEnabled = false; s.PwnedEnabled = false })
	if _, err := off.Auth.GetClientApplication(ctx, domain.CLIClientID); !domain.Is(err, domain.KindNotFound, "") {
		t.Errorf("tắt client CLI: %v", err)
	}
}

func TestAuthServiceUsesHIBPWhenEnabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// mọi hash đều "bị rò rỉ": trả mọi hậu tố khả dĩ không cần; trả dòng khớp bằng cách phản chiếu không khả thi nên
		// trả 500 cho đường dẫn bất kỳ để kiểm fail đóng.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newContainer(t, func(s *config.Settings) { s.PwnedEnabled = true; s.PwnedURL = srv.URL; s.PwnedTimeout = time.Second })
	err := c.Auth.CheckPwned(context.Background(), "password123")
	if !domain.Is(err, domain.KindInternal, "") {
		t.Fatalf("dịch vụ HIBP lỗi phải fail đóng: %v", err)
	}
}

func TestAuthServiceConfigErrors(t *testing.T) {
	for name, mut := range map[string]func(*config.Settings){
		"JSON client hỏng":  func(s *config.Settings) { s.DefaultOAuthClients = `[` },
		"client thiếu id":   func(s *config.Settings) { s.DefaultOAuthClients = `[{"name":"x"}]` },
		"bcrypt cost ngoài": func(s *config.Settings) { s.BcryptCost = 2 },
	} {
		if _, err := New(context.Background(), settings(t, mut)); err == nil {
			t.Errorf("%s: phải lỗi", name)
		}
	}
}

// Quyết định D2: dữ liệu của auth (User, Login, UserSecurityRequest...) nằm trong database hoặc SCHEMA RIÊNG, không lẫn với kho
// lâm sàng của fhir-server dù hai bên dùng cùng thư viện lưu trữ.
func TestAuthDataIsIsolatedFromClinicalStore(t *testing.T) {
	ctx := context.Background()
	auth := newContainer(t, nil)

	clinicalDB, err := database.Open(ctx, database.Config{URL: "sqlite://" + filepath.Join(t.TempDir(), "fhir.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer clinicalDB.Close()
	clinical, err := fhirstore.New(clinicalDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := clinical.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	created := map[string]string{}
	for _, r := range []fhir.Resource{
		&fhir.User{ResourceType: "User", Email: "an@example.org", PasswordHash: "$2b$10$bí-mật"},
		&fhir.Login{ResourceType: "Login", AuthMethod: "password", AuthTime: "2026-01-01T00:00:00Z", Code: "mã-đăng-nhập"},
		&fhir.UserSecurityRequest{ResourceType: "UserSecurityRequest", Type: "reset", Secret: "bí-mật"},
	} {
		out, err := auth.Store.CreateResource(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		created[r.GetResourceType()] = out.GetID()
	}
	for rt, id := range created {
		if _, err := clinical.ReadResource(ctx, rt, id); err == nil {
			t.Errorf("%s/%s KHÔNG được đọc được từ kho lâm sàng", rt, id)
		}
		res, err := clinical.Search(ctx, &fhirsearch.Request{ResourceType: rt, Count: 10, Total: "none"})
		if err != nil || len(res.Matches) != 0 {
			t.Errorf("kho lâm sàng không được có %s: %v %v", rt, res, err)
		}
		if _, err := auth.Store.ReadResource(ctx, rt, id); err != nil {
			t.Errorf("kho auth phải có %s/%s: %v", rt, id, err)
		}
	}
	if !strings.Contains(config.Default().DatabaseURL, "auth") {
		t.Error("database mặc định của auth phải có tên riêng, khác của fhir-server")
	}
}
