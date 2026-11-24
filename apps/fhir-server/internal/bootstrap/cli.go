package bootstrap

import (
	"context"
	"fmt"
	"io"

	"trivita-fhir/apps/fhir-server/internal/config"
	"trivita-fhir/pkg/database"
	"trivita-fhir/pkg/fhirauth"
	"trivita-fhir/pkg/fhirbinary"
	"trivita-fhir/pkg/fhirrouter"
	"trivita-fhir/pkg/fhirseed"
	"trivita-fhir/pkg/fhirstore"
	"trivita-fhir/pkg/fhirstore/migrations"
)

// Migrate chạy lệnh migration: "up" | "down" (một bước) | "status".
func Migrate(ctx context.Context, s config.Settings, action string, out io.Writer) error {
	url := s.DatabaseURL
	if s.DatabaseMigrateURL != "" { // role migrate riêng (có DDL); app chạy bằng role chỉ có DML
		url = s.DatabaseMigrateURL
	}
	db, err := database.Open(ctx, database.Config{URL: url, Echo: s.DatabaseEcho, RequireSchema: s.DatabaseRequireSchema})
	if err != nil {
		return err
	}
	defer db.Close()
	switch action {
	case "up":
		return db.Migrate(ctx, migrations.FS, fhirstore.MigrationTable)
	case "down":
		return db.Rollback(ctx, migrations.FS, fhirstore.MigrationTable)
	case "status":
		n, err := db.Pending(ctx, migrations.FS, fhirstore.MigrationTable)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "migration chưa áp dụng: %d\n", n)
		return err
	}
	return fmt.Errorf("lệnh migrate không hợp lệ: %q (up|down|status)", action)
}

// AssignProject gán project cho resource chưa có project (dữ liệu ghi trước khi bật tách theo project).
// Chạy trực tiếp trên database, không qua HTTP; xem fhirauth.AssignProject.
func AssignProject(ctx context.Context, s config.Settings, project string, types []string, dryRun bool, out io.Writer) error {
	if project == "" {
		return fmt.Errorf("thiếu -project")
	}
	db, err := database.Open(ctx, database.Config{URL: s.DatabaseURL, Echo: s.DatabaseEcho, RequireSchema: s.DatabaseRequireSchema})
	if err != nil {
		return err
	}
	defer db.Close()
	store, err := fhirstore.New(db)
	if err != nil {
		return err
	}
	if db.IsInMemory() {
		return fmt.Errorf("assign-project cần database lưu bền, không dùng được với bộ nhớ")
	}
	if n, err := store.Pending(ctx); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("schema chưa được migrate: chạy `fhir-server migrate up` trước")
	}
	rep, err := fhirauth.AssignProject(ctx, store, project, types, dryRun)
	verb := "đã gán"
	if dryRun {
		verb = "sẽ gán (dry-run)"
	}
	fmt.Fprintf(out, "quét %d resource, %s project %q cho %d resource", rep.Scanned, verb, project, rep.Total())
	for rt, n := range rep.Assigned {
		fmt.Fprintf(out, "\n  %s: %d", rt, n)
	}
	fmt.Fprintln(out)
	return err
}

// SweepBinary xóa nội dung Binary mồ côi trong kho (khóa không còn phiên bản Binary nào trong CSDL, quá ân hạn). Chạy trực tiếp trên
// CSDL và kho, không qua HTTP; xem fhirbinary.Sweep. Chỉ dùng được khi FHIR_BINARY_STORAGE bật.
func SweepBinary(ctx context.Context, s config.Settings, opt fhirbinary.SweepOptions, out io.Writer) error {
	if !s.StorageEnabled() {
		return fmt.Errorf("sweep-binary cần FHIR_BINARY_STORAGE")
	}
	db, err := database.Open(ctx, database.Config{URL: s.DatabaseURL, Echo: s.DatabaseEcho, RequireSchema: s.DatabaseRequireSchema})
	if err != nil {
		return err
	}
	defer db.Close()
	if db.IsInMemory() {
		return fmt.Errorf("sweep-binary cần database lưu bền, không dùng được với bộ nhớ")
	}
	store, err := fhirstore.New(db)
	if err != nil {
		return err
	}
	if n, err := store.Pending(ctx); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("schema chưa được migrate: chạy `fhir-server migrate up` trước")
	}
	bs, closer, err := openStore(s)
	if err != nil {
		return err
	}
	if closer != nil {
		defer closer()
	}
	rep, err := fhirbinary.Sweep(ctx, bs, store, opt)
	verb := "đã xóa"
	if opt.DryRun {
		verb = "sẽ xóa (dry-run)"
	}
	n := rep.Deleted
	if opt.DryRun {
		n = rep.Orphans
	}
	fmt.Fprintf(out, "duyệt %d khóa: %d còn dùng, %d còn trong ân hạn, %d mồ côi, %s %d, %d lỗi, dọn %d tệp tạm\n", rep.Scanned, rep.Live, rep.Young, rep.Orphans, verb, n, rep.Errors, rep.TempRemoved)
	if opt.DryRun {
		for _, k := range rep.Keys {
			fmt.Fprintln(out, "  ", k)
		}
	}
	if err == nil && rep.Errors > 0 {
		err = fmt.Errorf("%d lỗi khi quét (xem log); các khóa đó được giữ lại", rep.Errors)
	}
	return err
}

// SeedDefinitions nạp định nghĩa dựng sẵn (SearchParameter, StructureDefinition, ValueSet...) vào CSDL cho một project, chạy trực
// tiếp trên CSDL, không qua HTTP; xem fhirseed.Seed. Project rỗng: ghi không gán project (chế độ một tenant).
func SeedDefinitions(ctx context.Context, s config.Settings, opt fhirseed.Options, out io.Writer) error {
	db, err := database.Open(ctx, database.Config{URL: s.DatabaseURL, Echo: s.DatabaseEcho, RequireSchema: s.DatabaseRequireSchema})
	if err != nil {
		return err
	}
	defer db.Close()
	if db.IsInMemory() {
		return fmt.Errorf("seed cần database lưu bền, không dùng được với bộ nhớ")
	}
	store, err := fhirstore.New(db)
	if err != nil {
		return err
	}
	if n, err := store.Pending(ctx); err != nil {
		return err
	} else if n > 0 {
		return fmt.Errorf("schema chưa được migrate: chạy `fhir-server migrate up` trước")
	}
	var repo fhirrouter.Repository = store
	if opt.Project != "" {
		repo = fhirauth.Scope(store)
	}
	rep, err := fhirseed.Seed(ctx, repo, opt)
	verb := "đã ghi"
	if opt.DryRun {
		verb = "sẽ ghi (dry-run)"
	}
	for _, t := range fhirseed.AllTypes {
		if rep.Created[t]+rep.Updated[t]+rep.Unchanged[t]+rep.Failed[t] > 0 {
			fmt.Fprintf(out, "%s: %s mới %d, cập nhật %d, không đổi %d, lỗi %d\n", t, verb, rep.Created[t], rep.Updated[t], rep.Unchanged[t], rep.Failed[t])
		}
	}
	if len(rep.Missing) > 0 {
		fmt.Fprintf(out, "không có tệp: %v\n", rep.Missing)
	}
	for _, e := range rep.Errors {
		fmt.Fprintln(out, "  lỗi:", e)
	}
	if err == nil {
		for _, n := range rep.Failed {
			if n > 0 {
				err = fmt.Errorf("có resource nạp lỗi (xem trên)")
				break
			}
		}
	}
	return err
}
