// Command fhir-cli là công cụ dòng lệnh cho fhir-server: đọc, tìm kiếm, ghi, xóa, import NDJSON/Bundle, bulk export, reindex.
//
//	fhir-cli -base http://localhost:8005/fhir/R4 -token $TOKEN search Patient 'family=smith' -all -ndjson
//	fhir-cli import -f patients.ndjson
//	fhir-cli export -o out -level patient
//
// Xem `fhir-cli help`.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"trivita-fhir/apps/fhir-cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], cli.Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	stop()
	os.Exit(code)
}
