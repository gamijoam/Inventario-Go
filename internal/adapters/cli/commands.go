package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/application/admin"
	"github.com/gamijoam/inventario-go/internal/platform/buildinfo"
)

type versionPayload struct {
	Schema    string `json:"schema"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Modified  bool   `json:"modified"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

func newVersionCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Muestra la versión del ejecutable",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bi := buildinfo.Get()
			if flags.json {
				return writeJSON(cmd.OutOrStdout(), versionPayload{
					Schema: "balanzapro.version/v1", Version: bi.Version, Commit: bi.Commit,
					BuildDate: bi.Date, Modified: bi.Modified, GoVersion: bi.GoVersion, OS: bi.OS, Arch: bi.Arch,
				})
			}
			commit := orUnknown(bi.Commit)
			if bi.Modified {
				commit += " (con cambios locales)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "balanzapro %s\ncommit:     %s\ncompilado:  %s\ngo:         %s %s/%s\n",
				bi.Version, commit, orUnknown(bi.Date), bi.GoVersion, bi.OS, bi.Arch)
			return nil
		},
	}
}

type doctorPayload struct {
	Schema  string        `json:"schema"`
	Healthy bool          `json:"healthy"`
	Checks  []checkResult `json:"checks"`
}

type checkResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func newDoctorCmd(env Env, flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnostica el entorno sin mostrar credenciales",
		Long: "Ejecuta verificaciones del entorno. Códigos de salida: 0 sin fallos\n" +
			"(puede haber advertencias), 3 si alguna verificación falló.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := admin.RunDoctor(cmd.Context(), doctorChecks(env, flags))
			out := cmd.OutOrStdout()
			if flags.json {
				payload := doctorPayload{Schema: "balanzapro.doctor/v1", Healthy: report.Healthy(), Checks: []checkResult{}}
				for _, r := range report.Checks {
					payload.Checks = append(payload.Checks, checkResult{Name: r.Name, Status: string(r.Status), Detail: r.Detail})
				}
				if err := writeJSON(out, payload); err != nil {
					return err
				}
			} else {
				for _, r := range report.Checks {
					fmt.Fprintf(out, "%s %-10s %s\n", textStatus(r.Status), r.Name, r.Detail)
				}
				ok, warn, fail := report.Counts()
				fmt.Fprintf(out, "\n%d correctas, %d advertencias, %d fallos\n", ok, warn, fail)
			}
			if !report.Healthy() {
				return checkFailed()
			}
			return nil
		},
	}
}

// doctorChecks combina las verificaciones del dominio con las de terminal,
// que solo la CLI conoce. Las verificaciones de configuración y base de
// datos se agregan con BP-M0-003 y BP-M0-004.
func doctorChecks(env Env, flags *globalFlags) []admin.Check {
	checks := admin.DomainChecks()
	return append(checks, admin.Check{
		Name: "terminal",
		Run: func(context.Context) (admin.Status, string) {
			mode := "no interactiva (sin TUI)"
			if env.Interactive {
				mode = "interactiva"
			}
			color := "activado"
			if colorDisabled(env, flags) {
				color = "desactivado"
			}
			return admin.StatusOK, mode + ", color " + color
		},
	})
}

// textStatus usa etiquetas ASCII para funcionar en cualquier consola.
func textStatus(s admin.Status) string {
	switch s {
	case admin.StatusOK:
		return "[ OK ]"
	case admin.StatusWarn:
		return "[WARN]"
	default:
		return "[FAIL]"
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "desconocido"
	}
	return s
}
