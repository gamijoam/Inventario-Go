// Package timezone fija la política horaria DEC-006.TZ: los instantes se
// guardan y sincronizan en UTC y se presentan en America/Caracas.
package timezone

import (
	"time"
	// Incrusta la base IANA para que America/Caracas cargue también en
	// Windows o equipos sin zoneinfo del sistema.
	_ "time/tzdata"
)

const CaracasName = "America/Caracas"

var caracas = mustLoad(CaracasName)

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// Caracas devuelve la zona comercial por defecto.
func Caracas() *time.Location { return caracas }

// InCaracas presenta un instante en hora de Caracas sin cambiarlo.
func InCaracas(t time.Time) time.Time { return t.In(caracas) }

// ToUTC normaliza un instante para persistencia y sincronización.
func ToUTC(t time.Time) time.Time { return t.UTC() }
