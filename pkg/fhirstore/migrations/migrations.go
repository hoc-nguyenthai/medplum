// Package migrations chứa migration goose của fhirstore theo từng dialect (postgres, sqlite).
package migrations

import "embed"

// FS có cấu trúc <dialect>/*.sql, dùng với database.DB.Migrate.
//
//go:embed postgres sqlite
var FS embed.FS
