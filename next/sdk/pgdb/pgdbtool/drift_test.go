package pgdbtool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pgdb/pgdbtool"
	"github.com/Sub2API-Devs/sup2api/next/sdk/testpg"
)

// item is the model the migration of migrate() creates, exactly.
type item struct {
	ID      int64              `gorm:"primaryKey"`
	Name    string             `gorm:"not null;default:'';index:item_name_idx"`
	Tags    pgdb.Array[string] `gorm:"type:text[];not null;default:'{}'"`
	Doc     json.RawMessage    `gorm:"type:jsonb;not null;default:'{}'"`
	Until   time.Time          `gorm:"not null;default:'-infinity'"`
	Created time.Time          `gorm:"not null;default:now()"`
}

func (item) TableName() string { return "item" }

const itemSQL = `CREATE TABLE item (
    id      bigserial   PRIMARY KEY,
    name    text        NOT NULL DEFAULT '',
    tags    text[]      NOT NULL DEFAULT '{}',
    doc     jsonb       NOT NULL DEFAULT '{}',
    until   timestamptz NOT NULL DEFAULT '-infinity',
    created timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX item_name_idx ON item (name);`

// migrate writes the migrations and returns them as Drift reads them.
func migrate(t *testing.T, files ...string) []pgdbtool.Migration {
	t.Helper()
	dir := t.TempDir()
	for i, f := range files {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d_m.sql", i+1)), []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := pgdbtool.ReadMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func drift(t *testing.T, migrations []pgdbtool.Migration, models ...any) *pgdbtool.DriftReport {
	t.Helper()
	r, err := pgdbtool.Drift(context.Background(), testpg.URL(t), migrations, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDriftCleanWhenModelsMatch(t *testing.T) {
	if r := drift(t, migrate(t, itemSQL), &item{}); !r.Clean() {
		t.Fatalf("drift = %+v", r)
	}
}

func TestDriftCreatesWhatIsMissing(t *testing.T) {
	r := drift(t, migrate(t), &item{})
	if len(r.Statements) != 2 || !strings.HasPrefix(r.Statements[0], `CREATE TABLE "item"`) ||
		!strings.Contains(r.Statements[1], `"item_name_idx"`) || len(r.Unmapped) != 0 {
		t.Fatalf("drift = %+v", r)
	}
}

// itemV2 adds a column and an index, drops tags and widens nothing.
type itemV2 struct {
	ID      int64           `gorm:"primaryKey"`
	Name    string          `gorm:"not null;default:'';index:item_name_idx"`
	Doc     json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
	Until   time.Time       `gorm:"not null;default:'-infinity'"`
	Created time.Time       `gorm:"not null;default:now();index:item_created_idx"`
	Score   int64           `gorm:"not null;default:0"`
}

func (itemV2) TableName() string { return "item" }

func TestDriftReportsAdditionsAndUnmapped(t *testing.T) {
	r := drift(t, migrate(t, itemSQL, `CREATE TABLE extra (id int); CREATE INDEX item_old_idx ON item (until);`), &itemV2{})
	want := []string{
		`ALTER TABLE "item" ADD "score" bigint NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS "item_created_idx" ON "item" ("created")`,
	}
	if !slices.Equal(r.Statements, want) {
		t.Errorf("statements = %q", r.Statements)
	}
	if want := []string{"column item.tags", "index item.item_old_idx", "table extra"}; !slices.Equal(r.Unmapped, want) {
		t.Errorf("unmapped = %q", r.Unmapped)
	}
}

// itemText changes the type of name.
type itemText struct {
	ID      int64              `gorm:"primaryKey"`
	Name    string             `gorm:"type:varchar(20);not null;default:'';index:item_name_idx"`
	Tags    pgdb.Array[string] `gorm:"type:text[];not null;default:'{}'"`
	Doc     json.RawMessage    `gorm:"type:jsonb;not null;default:'{}'"`
	Until   time.Time          `gorm:"not null;default:'-infinity'"`
	Created time.Time          `gorm:"not null;default:now()"`
}

func (itemText) TableName() string { return "item" }

func TestDriftReportsTypeChanges(t *testing.T) {
	r := drift(t, migrate(t, itemSQL), &itemText{})
	if len(r.Statements) != 1 || !strings.Contains(r.Statements[0], `ALTER COLUMN "name" TYPE varchar(20)`) {
		t.Fatalf("statements = %q", r.Statements)
	}
}

// tagged has defaults GORM cannot compare as text: an ARRAY constructor
// reads ARRAY['a' to it, cut at the first "::".
type tagged struct {
	ID    int64              `gorm:"primaryKey"`
	Tags  pgdb.Array[string] `gorm:"type:text[];not null;default:ARRAY['a','b']"`
	Kind  string             `gorm:"type:varchar(8);not null;default:'x'"`
	Score int64              `gorm:"not null;default:0"`
}

func (tagged) TableName() string { return "tagged" }

const taggedSQL = `CREATE TABLE tagged (
    id    bigserial  PRIMARY KEY,
    tags  text[]     NOT NULL DEFAULT ARRAY['a', 'b'],
    kind  varchar(8) NOT NULL DEFAULT 'x'::character varying,
    score bigint     NOT NULL DEFAULT 0
);`

func TestDriftComparesDefaultsByMeaning(t *testing.T) {
	if r := drift(t, migrate(t, taggedSQL), &tagged{}); !r.Clean() {
		t.Fatalf("same defaults: %+v", r)
	}
	r := drift(t, migrate(t, strings.Replace(taggedSQL, "ARRAY['a', 'b']", "ARRAY['a']", 1)), &tagged{})
	if !slices.Equal(r.Statements, []string{`ALTER TABLE "tagged" ALTER COLUMN "tags" SET DEFAULT ARRAY['a','b']`}) || len(r.Unmapped) != 0 {
		t.Fatalf("changed default: %+v", r)
	}
}

// child maps the columns of the table below, none of its constraints.
type child struct {
	ID     int64 `gorm:"primaryKey;autoIncrement:false"`
	ItemID *int64
	Code   *string
}

func (child) TableName() string { return "child" }

const childSQL = `CREATE TABLE child (
    id      bigint PRIMARY KEY,
    item_id bigint CONSTRAINT child_item_id_fkey REFERENCES item (id),
    code    text UNIQUE,
    CHECK (id > 0)
);`

func TestDriftReportsUnmappedObjects(t *testing.T) {
	r := drift(t, migrate(t, itemSQL, childSQL, `
ALTER TABLE item ADD CONSTRAINT item_name_len CHECK (length(name) < 100);
CREATE INDEX item_lower_name_idx ON item (lower(name));
CREATE VIEW item_names AS SELECT name FROM item;
CREATE SEQUENCE item_seq;
CREATE FUNCTION item_count(least_id bigint) RETURNS bigint LANGUAGE sql AS 'SELECT count(*) FROM item WHERE id >= least_id';`),
		&item{}, &child{})
	want := []string{
		"constraint child.child_code_key UNIQUE (code)",
		"constraint child.child_id_check CHECK ((id > 0))",
		"constraint child.child_item_id_fkey FOREIGN KEY (item_id) REFERENCES item(id)",
		"constraint item.item_name_len CHECK ((length(name) < 100))",
		"function item_count(least_id bigint)",
		"index item.item_lower_name_idx",
		"sequence item_seq",
		"view item_names",
	}
	if len(r.Statements) != 0 || !slices.Equal(r.Unmapped, want) {
		t.Fatalf("drift = %+v", r)
	}
}

// mappedChild maps them all.
type mappedChild struct {
	ID     int64   `gorm:"primaryKey;autoIncrement:false;check:child_id_check,id > 0"`
	ItemID *int64  `gorm:"index:child_item_idx"`
	Code   *string `gorm:"unique"`
	Item   *item   `gorm:"foreignKey:ItemID;constraint:child_item_id_fkey,"`
}

func (mappedChild) TableName() string { return "child" }

func TestDriftMapsConstraintsByName(t *testing.T) {
	r := drift(t, migrate(t, itemSQL, childSQL, `CREATE INDEX child_item_idx ON child (item_id);`), &item{}, &mappedChild{})
	if !r.Clean() {
		t.Fatalf("drift = %+v", r)
	}
}

// A model that drops `unique` from a field whose constraint the migration
// named <table>_<column>_key: GORM would drop uni_child_code, which does not
// exist. The constraint is reported instead, by its name.
func TestDriftReportsDroppedUnique(t *testing.T) {
	r := drift(t, migrate(t, `CREATE TABLE child (id bigint PRIMARY KEY, item_id bigint, code text UNIQUE)`), &child{})
	if len(r.Statements) != 0 || !slices.Equal(r.Unmapped, []string{"constraint child.child_code_key UNIQUE (code)"}) {
		t.Fatalf("drift = %+v", r)
	}
}

// The drift AutoMigrate does not see (review 1116 F1, F2): each model is
// reported against its migration although AutoMigrate would change nothing.
type (
	notNullModel struct {
		ID   int64
		Name string `gorm:"type:text;not null"`
	}
	timestamptzModel struct {
		ID int64
		At time.Time `gorm:"not null"`
	}
	jsonbModel struct {
		ID  int64
		Doc string `gorm:"type:jsonb"`
	}
	arrayModel struct {
		ID   int64
		Tags pgdb.Array[string] `gorm:"type:text[]"`
	}
	timestampModel struct {
		ID int64
		At time.Time `gorm:"type:timestamp"`
	}
	bigintKeyModel struct {
		ID int64
		X  string `gorm:"type:text"`
	}
	compositeKeyModel struct {
		A int64 `gorm:"primaryKey;autoIncrement:false"`
		B int64 `gorm:"primaryKey;autoIncrement:false"`
	}
	uniqueIndexModel struct {
		ID   int64
		Code string `gorm:"type:text;uniqueIndex:t_code_idx"`
	}
	twoColumnIndexModel struct {
		ID int64
		A  string `gorm:"type:text;index:t_ab_idx,priority:1"`
		B  string `gorm:"type:text;index:t_ab_idx,priority:2"`
	}
	checkModel struct {
		ID int64
		N  int64 `gorm:"check:t_n_chk,n > 0"`
	}
	partialIndexModel struct {
		ID   int64
		Code string `gorm:"type:text;uniqueIndex:t_code_idx,where:code <> ''"`
	}
	plainIndexModel struct {
		ID   int64
		Code string `gorm:"type:text;index:t_code_idx"`
	}
	cascadeModel struct {
		ID       int64
		ParentID int64           `gorm:"not null"`
		Parent   *bigintKeyModel `gorm:"foreignKey:ParentID;constraint:t_parent_fkey,OnDelete:CASCADE"`
	}
	numericModel struct {
		ID  int64
		Amt float64 `gorm:"type:numeric(10,2)"`
	}
	nullableModel struct {
		ID   int64
		Name *string `gorm:"type:text"`
	}
	noDefaultModel struct {
		ID   int64
		Name string `gorm:"type:text;not null"`
	}
	uniqueModel struct {
		ID   int64
		Code string `gorm:"type:text;unique"`
	}
	textModel struct {
		ID   int64
		Code string `gorm:"type:text"`
	}
)

func (notNullModel) TableName() string        { return "t" }
func (timestamptzModel) TableName() string    { return "t" }
func (jsonbModel) TableName() string          { return "t" }
func (arrayModel) TableName() string          { return "t" }
func (timestampModel) TableName() string      { return "t" }
func (bigintKeyModel) TableName() string      { return "t" }
func (compositeKeyModel) TableName() string   { return "t" }
func (uniqueIndexModel) TableName() string    { return "t" }
func (twoColumnIndexModel) TableName() string { return "t" }
func (checkModel) TableName() string          { return "t" }
func (partialIndexModel) TableName() string   { return "t" }
func (plainIndexModel) TableName() string     { return "t" }
func (cascadeModel) TableName() string        { return "c" }
func (numericModel) TableName() string        { return "t" }
func (nullableModel) TableName() string       { return "t" }
func (noDefaultModel) TableName() string      { return "t" }
func (uniqueModel) TableName() string         { return "t" }
func (textModel) TableName() string           { return "t" }

func TestDriftComparesCatalogs(t *testing.T) {
	for _, c := range []struct {
		name, sql string
		models    []any
		want      string // in Differences or Unmapped
	}{
		{"not null missing", `CREATE TABLE t (id bigserial PRIMARY KEY, name text)`, []any{&notNullModel{}},
			"column t.name: migrations: text; models: text NOT NULL"},
		{"timestamp for timestamptz", `CREATE TABLE t (id bigserial PRIMARY KEY, at timestamp NOT NULL)`, []any{&timestamptzModel{}},
			"column t.at: migrations: timestamp without time zone NOT NULL; models: timestamp with time zone NOT NULL"},
		{"json for jsonb", `CREATE TABLE t (id bigserial PRIMARY KEY, doc json)`, []any{&jsonbModel{}},
			"column t.doc: migrations: json; models: jsonb"},
		{"text for text[]", `CREATE TABLE t (id bigserial PRIMARY KEY, tags text)`, []any{&arrayModel{}},
			"column t.tags: migrations: text; models: text[]"},
		{"time for timestamp", `CREATE TABLE t (id bigserial PRIMARY KEY, at time)`, []any{&timestampModel{}},
			"column t.at: migrations: time without time zone; models: timestamp without time zone"},
		{"serial for bigint key", `CREATE TABLE t (id serial PRIMARY KEY, x text)`, []any{&bigintKeyModel{}},
			"column t.id: migrations: integer NOT NULL DEFAULT nextval('t_id_seq'::regclass); models: bigint NOT NULL DEFAULT nextval('t_id_seq'::regclass)"},
		{"primary key columns", `CREATE TABLE t (a bigint PRIMARY KEY, b bigint NOT NULL)`, []any{&compositeKeyModel{}},
			"primary key t: migrations: PRIMARY KEY (a); models: PRIMARY KEY (a, b)"},
		{"unique index made plain", `CREATE TABLE t (id bigserial PRIMARY KEY, code text); CREATE INDEX t_code_idx ON t (code)`, []any{&uniqueIndexModel{}},
			"index t.t_code_idx: migrations: CREATE INDEX t_code_idx ON t USING btree (code); models: CREATE UNIQUE INDEX t_code_idx ON t USING btree (code)"},
		{"unique dropped", `CREATE TABLE t (id bigserial PRIMARY KEY, code text); CREATE UNIQUE INDEX t_code_idx ON t (code)`, []any{&plainIndexModel{}},
			"index t.t_code_idx: migrations: CREATE UNIQUE INDEX t_code_idx ON t USING btree (code); models: CREATE INDEX t_code_idx ON t USING btree (code)"},
		{"index columns", `CREATE TABLE t (id bigserial PRIMARY KEY, a text, b text); CREATE INDEX t_ab_idx ON t (a)`, []any{&twoColumnIndexModel{}},
			"index t.t_ab_idx: migrations: CREATE INDEX t_ab_idx ON t USING btree (a); models: CREATE INDEX t_ab_idx ON t USING btree (a, b)"},
		{"check expression", `CREATE TABLE t (id bigserial PRIMARY KEY, n bigint, CONSTRAINT t_n_chk CHECK (n >= -100))`, []any{&checkModel{}},
			"constraint t.t_n_chk: migrations: CHECK ((n >= '-100'::integer)); models: CHECK ((n > 0))"},
		{"partial index", `CREATE TABLE t (id bigserial PRIMARY KEY, code text); CREATE UNIQUE INDEX t_code_idx ON t (code)`, []any{&partialIndexModel{}},
			"index t.t_code_idx: migrations: CREATE UNIQUE INDEX t_code_idx ON t USING btree (code); models: CREATE UNIQUE INDEX t_code_idx ON t USING btree (code) WHERE (code <> ''::text)"},
		{"foreign key action", `CREATE TABLE t (id bigserial PRIMARY KEY, x text); CREATE TABLE c (id bigserial PRIMARY KEY, parent_id bigint NOT NULL CONSTRAINT t_parent_fkey REFERENCES t (id))`,
			[]any{&bigintKeyModel{}, &cascadeModel{}},
			"constraint c.t_parent_fkey: migrations: FOREIGN KEY (parent_id) REFERENCES t(id); models: FOREIGN KEY (parent_id) REFERENCES t(id) ON DELETE CASCADE"},
		{"numeric precision", `CREATE TABLE t (id bigserial PRIMARY KEY, amt numeric(12,4))`, []any{&numericModel{}},
			"column t.amt: migrations: numeric(12,4); models: numeric(10,2)"},
		{"not null relaxed", `CREATE TABLE t (id bigserial PRIMARY KEY, name text NOT NULL)`, []any{&nullableModel{}},
			"column t.name: migrations: text NOT NULL; models: text"},
		{"default dropped", `CREATE TABLE t (id bigserial PRIMARY KEY, name text NOT NULL DEFAULT 'x')`, []any{&noDefaultModel{}},
			"column t.name: migrations: text NOT NULL DEFAULT 'x'::text; models: text NOT NULL"},
		{"unique missing", `CREATE TABLE t (id bigserial PRIMARY KEY, code text)`, []any{&uniqueModel{}},
			"constraint t.uni_t_code: not in migrations (models: UNIQUE (code))"},
		{"unique index extra", `CREATE TABLE t (id bigserial PRIMARY KEY, code text); CREATE UNIQUE INDEX t_code_key ON t (code)`, []any{&textModel{}},
			"index t.t_code_key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := drift(t, migrate(t, c.sql), c.models...)
			if !slices.Contains(r.Differences, c.want) && !slices.Contains(r.Unmapped, c.want) {
				t.Fatalf("no %q in %+v", c.want, r)
			}
		})
	}
}

// priced is the table below, but for names and spelling.
type priced struct {
	ID    int64
	Code  string  `gorm:"type:text;not null;unique"`
	Price float64 `gorm:"type:numeric(12,4);not null;default:0"`
	Ref   string  `gorm:"type:varchar(20);not null;uniqueIndex:priced_ref_key"`
}

func (priced) TableName() string { return "priced" }

// What only the names or the spelling tell apart is the same: the unique
// constraints GORM and PostgreSQL name differently, a unique constraint and
// a unique index of the same columns, 0 and '0'::numeric.
func TestDriftIgnoresNamesAndSpelling(t *testing.T) {
	r := drift(t, migrate(t, `CREATE TABLE priced (id bigserial PRIMARY KEY, code text NOT NULL UNIQUE,
		price numeric(12,4) NOT NULL DEFAULT '0', ref varchar(20) NOT NULL, CONSTRAINT priced_ref_key UNIQUE (ref))`), &priced{})
	if !r.Clean() {
		t.Fatalf("drift = %+v", r)
	}
}

type escaping struct{ ID int64 }

func (escaping) TableName() string { return "public.drift_escape" }

// A model may not name a schema: AutoMigrate would leave the scratch schema.
func TestDriftRefusesSchemaQualifiedTables(t *testing.T) {
	_, err := pgdbtool.Drift(context.Background(), testpg.URL(t), migrate(t), []any{&escaping{}}, nil)
	if err == nil || !strings.Contains(err.Error(), "names a schema") {
		t.Fatalf("err = %v", err)
	}
}

// Drift runs in one transaction it rolls back: nothing is left on the server.
func TestDriftLeavesNothing(t *testing.T) {
	drift(t, migrate(t, itemSQL), &itemV2{})
	db, err := pgdb.Open(testpg.URL(t), pgdb.Options{MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer pgdb.Close(db) //nolint:errcheck
	var left []string
	err = db.Raw(`SELECT nspname FROM pg_namespace WHERE nspname LIKE ?`,
		fmt.Sprintf("sub2api_drift_%d_%%", os.Getpid())).Scan(&left).Error
	if err != nil || len(left) != 0 {
		t.Fatalf("left behind: %q (%v)", left, err)
	}
}

// Migrations run as the connecting user, like the host runs them as the
// core's; what they create or change outside the plugin schema is listed.
func TestDriftListsWhatMigrationsTouchOutsideTheSchema(t *testing.T) {
	for sql, want := range map[string]string{
		`CREATE TABLE public.drift_escape (id int)`:                                      "relation public.drift_escape",
		`CREATE SCHEMA drift_other`:                                                      "schema drift_other",
		`CREATE FUNCTION public.drift_f() RETURNS int LANGUAGE sql AS 'SELECT 1'`:        "function public.drift_f()",
		`CREATE TYPE public.drift_mood AS ENUM ('ok')`:                                   "type public.drift_mood",
		`CREATE TABLE drift_inside (id int); ALTER TABLE drift_inside SET SCHEMA public`: "relation public.drift_inside",
	} {
		r, err := pgdbtool.Drift(context.Background(), testpg.URL(t), migrate(t, sql), nil, nil)
		if err != nil || !slices.Contains(r.Unmapped, want+" (outside the plugin schema)") {
			t.Errorf("%s: drift = %+v, %v", sql, r, err)
		}
	}
}

const touchSQL = `CREATE TABLE t (id bigserial PRIMARY KEY, x text);
CREATE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END';
CREATE TRIGGER t_touch BEFORE UPDATE ON t FOR EACH ROW EXECUTE FUNCTION touch();
CREATE VIEW t_x AS SELECT x FROM t;
CREATE TYPE mood AS ENUM ('ok');`

// What no model describes is declared unmanaged by the model package.
func TestDriftUnmanaged(t *testing.T) {
	m := migrate(t, touchSQL)
	unmanaged := []string{"function touch()", "trigger t.t_touch", "view t_x", "type mood"}
	r := drift(t, m, &bigintKeyModel{})
	if !slices.Equal(r.Unmapped, unmanaged) && !slices.Equal(r.Unmapped, []string{"function touch()", "trigger t.t_touch", "type mood", "view t_x"}) {
		t.Fatalf("without unmanaged: %+v", r)
	}
	r, err := pgdbtool.Drift(context.Background(), testpg.URL(t), m, []any{&bigintKeyModel{}}, unmanaged)
	if err != nil || !r.Clean() {
		t.Fatalf("with unmanaged: %+v, %v", r, err)
	}
	r, err = pgdbtool.Drift(context.Background(), testpg.URL(t), m, []any{&bigintKeyModel{}}, append(unmanaged, "view gone"))
	if err != nil || !slices.Equal(r.Differences, []string{"unmanaged view gone: not in migrations"}) {
		t.Fatalf("stale unmanaged: %+v, %v", r, err)
	}
	if _, err := pgdbtool.Drift(context.Background(), testpg.URL(t), m, nil, []string{"table t"}); err == nil {
		t.Fatal("a table cannot be unmanaged")
	}
}
