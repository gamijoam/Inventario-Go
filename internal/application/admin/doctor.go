// Package admin agrupa casos de uso de administración y soporte técnico.
package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// Status es el resultado de una verificación de diagnóstico.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check es una verificación con nombre estable (se usa en la salida JSON).
// El detalle que devuelve Run nunca debe contener credenciales ni secretos.
type Check struct {
	Name string
	Run  func(context.Context) (Status, string)
}

// Result es el resultado de una verificación.
type Result struct {
	Name   string
	Status Status
	Detail string
}

// Report es el resultado de un diagnóstico, en el orden de las verificaciones.
type Report struct {
	Checks []Result
}

// Healthy es true si ninguna verificación falló; las advertencias no cuentan.
func (r Report) Healthy() bool {
	_, _, fail := r.Counts()
	return fail == 0
}

// Counts devuelve cuántas verificaciones terminaron en cada estado.
func (r Report) Counts() (ok, warn, fail int) {
	for _, c := range r.Checks {
		switch c.Status {
		case StatusOK:
			ok++
		case StatusWarn:
			warn++
		default:
			fail++
		}
	}
	return ok, warn, fail
}

// RunDoctor ejecuta las verificaciones en orden. Un pánico o un contexto
// cancelado se registran como fallo de esa verificación.
func RunDoctor(ctx context.Context, checks []Check) Report {
	report := Report{Checks: make([]Result, 0, len(checks))}
	for _, c := range checks {
		report.Checks = append(report.Checks, runCheck(ctx, c))
	}
	return report
}

func runCheck(ctx context.Context, c Check) (res Result) {
	res.Name = c.Name
	if err := ctx.Err(); err != nil {
		res.Status, res.Detail = StatusFail, "cancelado: "+err.Error()
		return res
	}
	defer func() {
		if p := recover(); p != nil {
			res.Status, res.Detail = StatusFail, fmt.Sprintf("error interno: %v", p)
		}
	}()
	res.Status, res.Detail = c.Run(ctx)
	return res
}

// DomainChecks verifica que la plataforma soporte las reglas del dominio:
// zona horaria comercial, aritmética decimal exacta y generación de IDs.
func DomainChecks() []Check {
	return []Check{
		{Name: "timezone", Run: checkTimezone},
		{Name: "decimal", Run: checkDecimal},
		{Name: "id", Run: checkID},
	}
}

func checkTimezone(context.Context) (Status, string) {
	loc, err := time.LoadLocation(timezone.CaracasName)
	if err != nil {
		return StatusFail, err.Error()
	}
	_, offset := time.Now().In(loc).Zone()
	return StatusOK, fmt.Sprintf("%s (UTC%+03d:%02d)", loc, offset/3600, abs(offset%3600)/60)
}

func checkDecimal(context.Context) (Status, string) {
	sum := decimal.MustParse("0.10").Add(decimal.MustParse("0.20"))
	if !sum.Equal(decimal.MustParse("0.30")) {
		return StatusFail, "0.10 + 0.20 = " + sum.String()
	}
	return StatusOK, "aritmética exacta (0.10 + 0.20 = 0.30)"
}

func checkID(context.Context) (Status, string) {
	a, err := id.New()
	if err != nil {
		return StatusFail, err.Error()
	}
	b, err := id.New()
	if err != nil {
		return StatusFail, err.Error()
	}
	if b.Compare(a) != 1 {
		return StatusFail, "los UUIDv7 no son crecientes"
	}
	return StatusOK, "UUIDv7 disponibles"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
