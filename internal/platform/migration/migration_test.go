package migration_test

import (
	"errors"
	"testing"
	"testing/fstest"

	"github.com/gamijoam/inventario-go/internal/platform/migration"
	"github.com/gamijoam/inventario-go/migrations"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func TestLoadPairsUpAndDownInOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0002_b.up.sql":   file("CREATE TABLE b (x INTEGER);"),
		"m/0001_a.up.sql":   file("CREATE TABLE a (x INTEGER);"),
		"m/0001_a.down.sql": file("DROP TABLE a;"),
	}
	migs, err := migration.Load(fsys, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 2 || migs[0].Version != 1 || migs[1].Version != 2 || migs[0].Name != "a" {
		t.Fatalf("migraciones = %+v", migs)
	}
	if migs[0].Down == "" || migs[1].Down != "" {
		t.Fatalf("down mal asociado: %+v", migs)
	}
	if len(migs[0].Checksum) != 64 || migs[0].Checksum == migs[1].Checksum {
		t.Fatalf("checksums = %q, %q", migs[0].Checksum, migs[1].Checksum)
	}
}

func TestLoadRejectsInvalidSets(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"hueco":             {"m/0001_a.up.sql": file("x"), "m/0003_c.up.sql": file("x")},
		"no empieza en 1":   {"m/0002_a.up.sql": file("x")},
		"sin up":            {"m/0001_a.down.sql": file("x")},
		"nombre inválido":   {"m/1_a.up.sql": file("x")},
		"mayúsculas":        {"m/0001_Crear.up.sql": file("x")},
		"nombres distintos": {"m/0001_a.up.sql": file("x"), "m/0001_b.down.sql": file("x")},
		"directorio vacío":  {"otro/0001_a.up.sql": file("x")},
	}
	for name, fsys := range cases {
		if _, err := migration.Load(fsys, "m"); !errors.Is(err, migration.ErrInvalidSet) {
			t.Errorf("%s: se esperaba ErrInvalidSet, obtuvo %v", name, err)
		}
	}
}

func TestEmbeddedMigrationsAreConsistentAcrossEngines(t *testing.T) {
	sqlite, err := migration.Load(migrations.FS, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	pg, err := migration.Load(migrations.FS, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if len(sqlite) == 0 || len(sqlite) != len(pg) {
		t.Fatalf("sqlite tiene %d migraciones y postgres %d", len(sqlite), len(pg))
	}
	for i := range sqlite {
		if sqlite[i].Version != pg[i].Version || sqlite[i].Name != pg[i].Name {
			t.Errorf("versión %d: sqlite %04d_%s, postgres %04d_%s", i+1, sqlite[i].Version, sqlite[i].Name, pg[i].Version, pg[i].Name)
		}
		if (sqlite[i].Down == "") != (pg[i].Down == "") {
			t.Errorf("%04d: la reversibilidad difiere entre motores", sqlite[i].Version)
		}
	}
}

func TestStatusProblem(t *testing.T) {
	if (migration.Status{}).Problem() != nil {
		t.Fatal("estado vacío sin problemas")
	}
	if err := (migration.Status{Modified: []int{1}}).Problem(); !errors.Is(err, migration.ErrChecksumMismatch) {
		t.Fatalf("Modified: %v", err)
	}
	if err := (migration.Status{Unknown: []int{9}}).Problem(); !errors.Is(err, migration.ErrDatabaseNewer) {
		t.Fatalf("Unknown: %v", err)
	}
}
