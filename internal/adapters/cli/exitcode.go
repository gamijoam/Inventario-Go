package cli

import "fmt"

// Códigos de salida estables. Cambiarlos rompe scripts: solo se agregan.
const (
	ExitOK = 0
	// ExitFailure: la operación falló por un error no clasificado.
	ExitFailure = 1
	// ExitUsage: comando, argumento o flag inválido; no se hizo nada.
	ExitUsage = 2
	// ExitCheckFailed: doctor encontró al menos una verificación fallida.
	ExitCheckFailed = 3
	// ExitConfig: la configuración (archivo o entorno) es inválida.
	ExitConfig = 4
)

// exitError asocia un error con su código de salida. Si silent es true, el
// comando ya informó el problema por su salida y no se imprime nada más.
type exitError struct {
	code   int
	kind   string
	msg    string
	silent bool
}

func (e *exitError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) error {
	return &exitError{code: ExitUsage, kind: "usage", msg: fmt.Sprintf(format, args...)}
}

func configError(err error) error {
	return &exitError{code: ExitConfig, kind: "config", msg: err.Error()}
}

func checkFailed() error {
	return &exitError{code: ExitCheckFailed, kind: "check_failed", msg: "diagnóstico con fallos", silent: true}
}
