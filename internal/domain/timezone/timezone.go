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

// DateLayout es el formato de una fecha comercial.
const DateLayout = "2006-01-02"

// DateOf devuelve la fecha comercial de Caracas del instante t, como la
// medianoche UTC de ese día.
func DateOf(t time.Time) time.Time {
	y, m, d := t.In(caracas).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ParseDate lee una fecha comercial AAAA-MM-DD.
func ParseDate(s string) (time.Time, error) {
	return time.Parse(DateLayout, s)
}
