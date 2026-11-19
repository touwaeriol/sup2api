package dbschema

import "testing"

func TestSchemaDSN(t *testing.T) {
	for dsn, want := range map[string]string{
		"postgres://app:secret@db:5432/app?sslmode=disable": "postgres://app:secret@db:5432/app?options=-c%20search_path%3Dplg_x&sslmode=disable",
		"postgres://app:secret@db/app":                      "postgres://app:secret@db/app?options=-c%20search_path%3Dplg_x",
		"host=db user=app password=secret dbname=app":       "host=db user=app password=secret dbname=app options='-c search_path=plg_x'",
	} {
		got, err := schemaDSN(dsn, "plg_x")
		if err != nil || got != want {
			t.Fatalf("schemaDSN(%s) = %s, %v\nwant %s", dsn, got, err, want)
		}
	}
}
