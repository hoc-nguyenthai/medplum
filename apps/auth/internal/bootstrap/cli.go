package bootstrap

import (
	"context"
	"fmt"
	"io"

	"trivita-fhir/apps/auth/internal/config"
	"trivita-fhir/pkg/database"
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
