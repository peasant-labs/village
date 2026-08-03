//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	if len(os.Args) != 4 {
		fail("usage: go run ../scripts/test-database-admin.go <reset|drop> <admin-url> <database>")
	}
	action, adminURL, database := os.Args[1], os.Args[2], os.Args[3]
	if !allowedDatabase(database) {
		fail("database %q is not an allowed isolated integration database; use the package database names owned by scripts/run-integration-gates.sh", database)
	}
	if action != "reset" && action != "drop" {
		fail("action %q is unsupported; use reset or drop", action)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		fail("connect to TEST_DATABASE_ADMIN_URL for %s failed: %v; restore PostgreSQL connectivity and CREATEDB authority", action, err)
	}
	defer conn.Close(ctx)

	identifier := pgx.Identifier{database}.Sanitize()
	if _, err = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+identifier+" WITH (FORCE)"); err != nil {
		fail("drop isolated database %s failed: %v; close unexpected sessions and verify the test role owns the database", database, err)
	}
	if action == "reset" {
		if _, err = conn.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
			fail("create isolated database %s failed: %v; grant the test role CREATEDB without changing production roles", database, err)
		}
	}
}

func allowedDatabase(database string) bool {
	switch database {
	case "village_ci_package":
		return true
	default:
		return false
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "scripts/test-database-admin.go: "+format+"\n", args...)
	os.Exit(1)
}
