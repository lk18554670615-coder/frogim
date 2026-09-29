// tenant-preflight never imports accounts or changes either database. Private
// DSNs are read only from explicitly named environment variables, not flags.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/linli/im/server/internal/legacyimport"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, env func(string) string, out, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("tenant-preflight", flag.ContinueOnError)
	// Do not echo unrecognized arguments: they might accidentally contain a DSN.
	flags.SetOutput(io.Discard)
	tenant := flags.String("tenant", "default", "default enterprise ID")
	path := flags.String("report", "", "optional new private JSON report file; never overwrite")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(out, "Usage: tenant-preflight [-tenant default] [-report new-private-report.json]")
		fmt.Fprintln(out, "Requires TENANCY_PREFLIGHT_ENV=development and explicit loopback PostgreSQL URLs in TENANCY_PREFLIGHT_SOURCE_DATABASE_URL and TENANCY_PREFLIGHT_PLATFORM_DATABASE_URL.")
		fmt.Fprintln(out, "Read-only. No import, migrations, activation or session revocation. Reports never overwrite existing files. Exit: 0=data checks passed (warnings may remain), 1=data conflict, 2=configuration/read/report failure.")
		return 0
	} else if err != nil || flags.NArg() != 0 {
		fmt.Fprintln(diagnostics, "PREFLIGHT_ARGUMENTS_INVALID (DSNs must use environment variables)")
		return 2
	}
	if env("TENANCY_PREFLIGHT_ENV") != "development" {
		fmt.Fprintln(diagnostics, "PREFLIGHT_LOCAL_PREVIEW_REQUIRED")
		return 2
	}
	source, err := legacyimport.OpenLocalReadOnly(ctx, env("TENANCY_PREFLIGHT_SOURCE_DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(diagnostics, "PREFLIGHT_SOURCE_UNAVAILABLE")
		return 2
	}
	defer source.Close()
	target, err := legacyimport.OpenLocalReadOnly(ctx, env("TENANCY_PREFLIGHT_PLATFORM_DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(diagnostics, "PREFLIGHT_PLATFORM_UNAVAILABLE")
		return 2
	}
	defer target.Close()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	report, err := legacyimport.Preflight(ctx, source, target, *tenant, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(diagnostics, err.Error())
		return 2
	}
	if *path != "" {
		if err = saveReport(*path, report); err != nil {
			fmt.Fprintln(diagnostics, "PREFLIGHT_REPORT_NOT_SAVED (existing files are never overwritten; a newly created partial report may remain after an I/O failure)")
			return 2
		}
	}
	fmt.Fprintln(out, report.Summary())
	fmt.Fprintln(out, "Read-only data check. No import, adoption, login activation, password export or session revocation performed. Cutover requires separate validation.")
	if !report.DataChecksPassed {
		return 1
	}
	return 0
}
func saveReport(path string, report legacyimport.Report) error {
	if path == "" || filepath.Ext(path) != ".json" {
		return errors.New("invalid report path")
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	// A caller chooses an existing private directory. Do not create directories,
	// overwrite reports, follow an existing symlink or emit a credential manifest.
	f, err := createPrivateReport(path)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
