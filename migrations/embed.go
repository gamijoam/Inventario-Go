// Package migrations contiene las migraciones SQL versionadas de cada motor.
//
// sqlite/ y postgres/ tienen la misma numeración y describen el mismo
// esquema lógico con SQL nativo de cada motor; la equivalencia se verifica
// en las pruebas de internal/adapters/persistence.
package migrations

import "embed"

//go:embed sqlite/*.sql postgres/*.sql
var FS embed.FS
