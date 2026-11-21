package main

import "testing"

// Bad usage stops before the configuration and the database are touched.
func TestRunAdminUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"recover-superadmin"}, {"recover-superadmin", "--email", "a@x.com", "extra"}, {"recover-superadmin", "--bogus"}, {"check-prices", "extra"}} {
		if code := runAdmin(args); code != 2 {
			t.Errorf("runAdmin(%q) = %d, want 2", args, code)
		}
	}
}
