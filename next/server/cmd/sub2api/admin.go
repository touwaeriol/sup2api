package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/app"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/authz"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/billing"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/config"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/iam"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

const adminUsage = "usage: sub2api admin recover-superadmin --email <existing user> [--reset-password] [--reset-2fa]\n" +
	"       sub2api admin check-prices"

// runAdmin is `sub2api admin <command>`: repairs and checks run on the
// server against the configured database while the core may be down. It runs
// no migrations and connects no Redis, and there is no HTTP counterpart.
// recover-superadmin needs exactly this version's migrations
// (app.VerifyCoreSchema).
func runAdmin(args []string) int {
	switch {
	case len(args) == 1 && args[0] == "check-prices":
		return runCheckPrices()
	case len(args) > 0 && args[0] == "recover-superadmin":
		return runRecoverSuperAdmin(args[1:])
	}
	fmt.Fprintln(os.Stderr, adminUsage)
	return 2
}

// runCheckPrices compiles every stored price expression with this version's
// engine (billing.CheckStoredExpressions). It only reads and does not need
// this version's schema: run it against the database before an upgrade.
func runCheckPrices() int {
	cfg, err := config.Load(runtime.GOOS)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sub2api admin: config:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sub2api admin: database:", err)
		return 1
	}
	defer db.Close()
	problems, err := billing.CheckStoredExpressions(ctx, db.Gorm)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sub2api admin:", err)
		return 1
	}
	for _, p := range problems {
		fmt.Printf("%s %s: %s\n", p.Table, p.Ref, p.Error)
	}
	if len(problems) > 0 {
		fmt.Printf("%d stored expressions do not compile\n", len(problems))
		return 1
	}
	fmt.Println("every stored expression compiles")
	return 0
}

// runRecoverSuperAdmin makes an existing user the only super admin (the
// other holder becomes an admin), for a lost super admin.
func runRecoverSuperAdmin(args []string) int {
	fs := flag.NewFlagSet("admin recover-superadmin", flag.ContinueOnError)
	email := fs.String("email", "", "existing user who becomes the only super admin")
	resetPassword := fs.Bool("reset-password", false, "set a random password and print it once")
	resetTwoFactor := fs.Bool("reset-2fa", false, "remove the user's two-factor authentication and passkeys")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *email == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, adminUsage)
		return 2
	}
	cfg, err := config.Load(runtime.GOOS)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sub2api admin: config:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sub2api admin: database:", err)
		return 1
	}
	defer db.Close()
	if err := app.VerifyCoreSchema(ctx, db); err != nil {
		fmt.Fprintln(os.Stderr, "sub2api admin: the database is not at this version's schema:", err)
		return 1
	}
	az := authz.New(authz.Deps{DB: db})
	idm := iam.New(iam.Deps{DB: db, Config: cfg, Authz: az, Owners: az})
	password, err := idm.RecoverSuperAdmin(audit.WithSource(ctx, "cli"), *email,
		iam.RecoverOptions{ResetPassword: *resetPassword, ResetTwoFactor: *resetTwoFactor})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sub2api admin:", err)
		return 1
	}
	fmt.Printf("%s is now the only super admin; the other holders are admins and every session of them all has ended.\n", *email)
	if password != "" {
		fmt.Printf("new password (shown once): %s\n", password)
	}
	fmt.Println("Signing in again works at once; running nodes end the old sessions and apply the new roles within 30 seconds (status cache and authz version poll).")
	return 0
}
