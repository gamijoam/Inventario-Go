package persistence_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// schema es la forma lógica de un esquema, independiente del motor: los
// tipos físicos difieren a propósito (UUID/TEXT, TIMESTAMPTZ/TEXT), pero
// tablas, columnas, nulabilidad, claves, FKs e índices deben coincidir.
type schema struct {
	Tables map[string]table
}

type table struct {
	Columns     []string // "nombre NOT NULL" o "nombre NULL", en orden
	PrimaryKey  []string
	ForeignKeys []string // "col -> tabla(col) ON DELETE regla"
	Indexes     []string // "nombre(cols) unique|nonunique", sin los implícitos
}

func TestSchemasAreEquivalentAcrossEngines(t *testing.T) {
	sq, _ := migrated(t, engines[0].setup(t))
	pg, _ := migrated(t, engines[1].setup(t))

	a, err := sqliteSchema(ctx(t), sq.DB)
	if err != nil {
		t.Fatal(err)
	}
	b, err := postgresSchema(ctx(t), pg.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tables) == 0 {
		t.Fatal("no se encontraron tablas")
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("los esquemas difieren:\nSQLite:     %+v\nPostgreSQL: %+v", a, b)
	}
}

func sqliteSchema(ctx context.Context, db *sql.DB) (schema, error) {
	s := schema{Tables: map[string]table{}}
	names, err := strings1(ctx, db, "SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return s, err
	}
	for _, name := range names {
		var tb table
		rows, err := db.QueryContext(ctx, "SELECT name, \"notnull\", pk FROM pragma_table_info(?) ORDER BY cid", name)
		if err != nil {
			return s, err
		}
		pk := map[int]string{}
		for rows.Next() {
			var col string
			var notNull, pkPos int
			if err := rows.Scan(&col, &notNull, &pkPos); err != nil {
				rows.Close()
				return s, err
			}
			tb.Columns = append(tb.Columns, column(col, notNull == 1 || pkPos > 0))
			if pkPos > 0 {
				pk[pkPos] = col
			}
		}
		rows.Close()
		for i := 1; i <= len(pk); i++ {
			tb.PrimaryKey = append(tb.PrimaryKey, pk[i])
		}

		rows, err = db.QueryContext(ctx, "SELECT \"from\", \"table\", \"to\", on_delete FROM pragma_foreign_key_list(?)", name)
		if err != nil {
			return s, err
		}
		for rows.Next() {
			var from, ref, to, onDelete string
			if err := rows.Scan(&from, &ref, &to, &onDelete); err != nil {
				rows.Close()
				return s, err
			}
			tb.ForeignKeys = append(tb.ForeignKeys, fmt.Sprintf("%s -> %s(%s) ON DELETE %s", from, ref, to, onDelete))
		}
		rows.Close()

		// origin 'c' = CREATE INDEX explícito; 'u' = UNIQUE de columna.
		rows, err = db.QueryContext(ctx, "SELECT name, \"unique\", origin FROM pragma_index_list(?) WHERE origin IN ('c', 'u')", name)
		if err != nil {
			return s, err
		}
		type idx struct {
			name, origin string
			unique       bool
		}
		var idxs []idx
		for rows.Next() {
			var i idx
			var u int
			if err := rows.Scan(&i.name, &u, &i.origin); err != nil {
				rows.Close()
				return s, err
			}
			i.unique = u == 1
			idxs = append(idxs, i)
		}
		rows.Close()
		for _, i := range idxs {
			cols, err := strings1(ctx, db, "SELECT name FROM pragma_index_info(?) ORDER BY seqno", i.name)
			if err != nil {
				return s, err
			}
			label := i.name
			if i.origin == "u" {
				label = "unique:" + strings.Join(cols, ",")
			}
			tb.Indexes = append(tb.Indexes, index(label, cols, i.unique))
		}
		sortTable(&tb)
		s.Tables[name] = tb
	}
	return s, nil
}

func postgresSchema(ctx context.Context, db *sql.DB) (schema, error) {
	s := schema{Tables: map[string]table{}}
	names, err := strings1(ctx, db, "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name")
	if err != nil {
		return s, err
	}
	for _, name := range names {
		var tb table
		rows, err := db.QueryContext(ctx, "SELECT column_name, is_nullable FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position", name)
		if err != nil {
			return s, err
		}
		for rows.Next() {
			var col, nullable string
			if err := rows.Scan(&col, &nullable); err != nil {
				rows.Close()
				return s, err
			}
			tb.Columns = append(tb.Columns, column(col, nullable == "NO"))
		}
		rows.Close()

		tb.PrimaryKey, err = strings1(ctx, db, `
SELECT a.attname FROM pg_index i
JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
WHERE i.indrelid = $1::regclass AND i.indisprimary ORDER BY k.ord`, name)
		if err != nil {
			return s, err
		}

		rows, err = db.QueryContext(ctx, `
SELECT a.attname, cf.relname, af.attname,
       CASE c.confdeltype WHEN 'r' THEN 'RESTRICT' WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' ELSE 'NO ACTION' END
FROM pg_constraint c
JOIN pg_class cf ON cf.oid = c.confrelid
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
JOIN pg_attribute af ON af.attrelid = c.confrelid AND af.attnum = c.confkey[1]
WHERE c.contype = 'f' AND c.conrelid = $1::regclass`, name)
		if err != nil {
			return s, err
		}
		for rows.Next() {
			var from, ref, to, onDelete string
			if err := rows.Scan(&from, &ref, &to, &onDelete); err != nil {
				rows.Close()
				return s, err
			}
			tb.ForeignKeys = append(tb.ForeignKeys, fmt.Sprintf("%s -> %s(%s) ON DELETE %s", from, ref, to, onDelete))
		}
		rows.Close()

		rows, err = db.QueryContext(ctx, `
SELECT ic.relname, i.indisunique, EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.indexrelid AND c.contype = 'u'),
       array_to_string(ARRAY(SELECT a.attname FROM unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
                             JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum ORDER BY k.ord), ',')
FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid
WHERE i.indrelid = $1::regclass AND NOT i.indisprimary`, name)
		if err != nil {
			return s, err
		}
		for rows.Next() {
			var idxName, cols string
			var unique, isConstraint bool
			if err := rows.Scan(&idxName, &unique, &isConstraint, &cols); err != nil {
				rows.Close()
				return s, err
			}
			label := idxName
			if isConstraint {
				label = "unique:" + cols
			}
			tb.Indexes = append(tb.Indexes, index(label, strings.Split(cols, ","), unique))
		}
		rows.Close()
		sortTable(&tb)
		s.Tables[name] = tb
	}
	return s, nil
}

func column(name string, notNull bool) string {
	if notNull {
		return name + " NOT NULL"
	}
	return name + " NULL"
}

func index(label string, cols []string, unique bool) string {
	kind := "nonunique"
	if unique {
		kind = "unique"
	}
	return fmt.Sprintf("%s(%s) %s", label, strings.Join(cols, ","), kind)
}

func sortTable(tb *table) {
	sort.Strings(tb.ForeignKeys)
	sort.Strings(tb.Indexes)
}

func strings1(ctx context.Context, db *sql.DB, q string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
