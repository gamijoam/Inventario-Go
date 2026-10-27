// Package cli implementa los comandos de balanzapro con Cobra.
//
// Contrato estable para automatización:
//   - --json escribe en stdout un único documento JSON con campo "schema"
//     versionado; nunca contiene secuencias ANSI.
//   - Los errores van a stderr (en JSON si se usó --json).
//   - Los códigos de salida están definidos en exitcode.go.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	"github.com/gamijoam/inventario-go/internal/application/admin"
	"github.com/gamijoam/inventario-go/internal/platform/buildinfo"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

// Env es todo lo que la CLI necesita del proceso; se inyecta para pruebas.
type Env struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
	// Getenv lee variables de entorno (NO_COLOR, etc.).
	Getenv func(string) string
	// Interactive indica si stdin y stdout son una terminal.
	Interactive bool
	// RunTUI abre la interfaz interactiva.
	RunTUI func(ctx context.Context, in io.Reader, out io.Writer, opts tui.Options) error
	// ConfigOptions describe dónde y cómo cargar la configuración;
	// explicitPath proviene de --config.
	ConfigOptions func(explicitPath string) config.LoadOptions
	// ReadPassword pide un secreto en la terminal sin eco. Puede ser nil si
	// no hay terminal; entonces solo se admite --password-stdin.
	ReadPassword func(prompt string) (string, error)
}

type globalFlags struct {
	json       bool
	noColor    bool
	configPath string
	logLevel   string
}

// Execute ejecuta la CLI con args (sin el nombre del programa) y devuelve el
// código de salida.
func Execute(ctx context.Context, args []string, env Env) int {
	var flags globalFlags
	root := newRootCmd(env, &flags)
	root.SetArgs(args)
	root.SetIn(env.In)
	root.SetOut(env.Out)
	root.SetErr(env.Err)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var exit *exitError
	if errors.As(err, &exit) && exit.silent {
		return exit.code
	}
	code := ExitFailure
	kind := "failure"
	switch {
	case errors.As(err, &exit):
		code, kind = exit.code, exit.kind
	case strings.HasPrefix(err.Error(), "required flag"):
		// Cobra no pasa los flags obligatorios faltantes por SetFlagErrorFunc.
		code, kind = ExitUsage, "usage"
	}
	// Si el análisis de flags falló, flags.json puede no estar asignado.
	if flags.json || containsFlag(args, "--json") {
		writeJSON(env.Err, errorPayload{Schema: "balanzapro.error/v1", Error: errorBody{Kind: kind, Message: err.Error()}})
	} else {
		fmt.Fprintf(env.Err, "Error: %s\n", err)
		if code == ExitUsage {
			fmt.Fprintln(env.Err, "Use 'balanzapro --help' para ver los comandos disponibles.")
		}
	}
	return code
}

func newRootCmd(env Env, flags *globalFlags) *cobra.Command {
	root := &cobra.Command{
		Use:   "balanzapro",
		Short: "Inventario y operación comercial",
		Long: "BalanzaPro: inventario y operación comercial.\n\n" +
			"Sin subcomando abre la interfaz interactiva (TUI). Los subcomandos\n" +
			"admiten --json para automatización.",
		Version:       buildinfo.Get().Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.json {
				return usageErrorf("la TUI no admite --json; use un subcomando, p. ej. 'balanzapro doctor --json'")
			}
			if !env.Interactive {
				return usageErrorf("la TUI requiere una terminal interactiva; use un subcomando o 'balanzapro --help'")
			}
			// La TUI ocupa la terminal: sin archivo de log configurado, los
			// registros se descartan en lugar de ir a stderr.
			rt, err := loadRuntime(env, flags, nil)
			if err != nil {
				return err
			}
			defer rt.close()
			rt.logger.Debug("TUI iniciada")
			opts := tui.Options{
				NoColor: colorDisabled(env, flags),
				Build:   buildinfo.Get(),
				Doctor:  func(ctx context.Context) admin.Report { return admin.RunDoctor(ctx, doctorChecks(env, flags)) },
			}
			return env.RunTUI(cmd.Context(), env.In, env.Out, opts)
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().BoolVar(&flags.json, "json", false, "salida JSON estable para automatización")
	root.PersistentFlags().BoolVar(&flags.noColor, "no-color", false, "desactiva colores (también con NO_COLOR)")
	root.PersistentFlags().StringVar(&flags.configPath, "config", "", "archivo de configuración (también BALANZAPRO_CONFIG)")
	root.PersistentFlags().StringVar(&flags.logLevel, "log-level", "", "nivel de log: debug, info, warn o error")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErrorf("%s", err) })

	root.AddCommand(newVersionCmd(flags), newDoctorCmd(env, flags), newConfigCmd(env, flags), newDBCmd(env, flags),
		newSetupCmd(env, flags), newAuthCmd(env, flags), newUserCmd(env, flags), newRoleCmd(env, flags),
		newUnitCmd(env, flags), newProductCmd(env, flags), newInventoryCmd(env, flags), newPurchaseCmd(env, flags),
		newOrgCmd(env, flags), newCompanyCmd(env, flags), newBranchCmd(env, flags), newWarehouseCmd(env, flags))
	return root
}

// colorDisabled aplica https://no-color.org: cualquier valor no vacío de
// NO_COLOR desactiva el color, igual que --no-color o la falta de terminal.
func colorDisabled(env Env, flags *globalFlags) bool {
	return flags.noColor || env.Getenv("NO_COLOR") != "" || !env.Interactive
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		if cmd.HasSubCommands() {
			return usageErrorf("comando desconocido %q para %q", args[0], cmd.CommandPath())
		}
		return usageErrorf("%q no admite argumentos, recibió %q", cmd.CommandPath(), args)
	}
	return nil
}

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == flag || strings.HasPrefix(a, flag+"=") && a != flag+"=false" {
			return true
		}
	}
	return false
}

type errorPayload struct {
	Schema string    `json:"schema"`
	Error  errorBody `json:"error"`
}

type errorBody struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
