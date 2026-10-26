package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/application/admin"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

type configPayload struct {
	Schema    string        `json:"schema"`
	Path      string        `json:"path"`
	FileFound bool          `json:"file_found"`
	Values    []configValue `json:"values"`
	Warnings  []string      `json:"warnings"`
}

type configValue struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
	EnvVar string `json:"env_var"`
}

func newConfigCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Consulta la configuración efectiva",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Muestra valores efectivos y su origen, con secretos redactados",
		Long: "Muestra la configuración efectiva. Prioridad: valores por defecto <\n" +
			"archivo TOML < variables BALANZAPRO_*. Código de salida 4 si es inválida.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := loadRuntime(env, flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer rt.close()
			l := rt.loaded
			display := l.DisplayValues()
			out := cmd.OutOrStdout()
			if flags.json {
				payload := configPayload{Schema: "balanzapro.config/v1", Path: l.Path, FileFound: l.FileFound, Warnings: l.Warnings}
				if payload.Warnings == nil {
					payload.Warnings = []string{}
				}
				for _, key := range config.Keys() {
					payload.Values = append(payload.Values, configValue{Key: key, Value: display[key], Source: string(l.Sources[key]), EnvVar: config.EnvVar(key)})
				}
				return writeJSON(out, payload)
			}
			fmt.Fprintf(out, "archivo: %s (%s)\n\n", l.Path, fileState(l))
			for _, key := range config.Keys() {
				fmt.Fprintf(out, "%-22s = %-40s [%s]\n", key, quoteOrEmpty(display[key]), l.Sources[key])
			}
			for _, w := range l.Warnings {
				fmt.Fprintf(out, "\nADVERTENCIA: %s\n", w)
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "path",
		Short: "Muestra la ruta del archivo de configuración en uso",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := env.ConfigOptions(flags.configPath)
			path := flags.configPath
			if path == "" {
				path = opts.Getenv("BALANZAPRO_CONFIG")
			}
			if path == "" {
				path = config.DefaultPaths(opts).ConfigFile
			}
			if flags.json {
				return writeJSON(cmd.OutOrStdout(), struct {
					Schema string `json:"schema"`
					Path   string `json:"path"`
				}{"balanzapro.config-path/v1", path})
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	})
	return cmd
}

// configCheck verifica que la configuración cargue y sea válida. No falla
// por la ausencia del archivo: los valores por defecto son válidos.
func configCheck(env Env, flags *globalFlags) admin.Check {
	return admin.Check{
		Name: "config",
		Run: func(context.Context) (admin.Status, string) {
			l, err := config.Load(env.ConfigOptions(flags.configPath))
			if err != nil {
				return admin.StatusFail, err.Error()
			}
			detail := fmt.Sprintf("%s (%s), base de datos %s", l.Path, fileState(l), l.Config.Database.Driver)
			if len(l.Warnings) > 0 {
				return admin.StatusWarn, detail + "; " + strings.Join(l.Warnings, "; ")
			}
			return admin.StatusOK, detail
		},
	}
}

func fileState(l config.Loaded) string {
	if l.FileFound {
		return "cargado"
	}
	return "no existe; valores por defecto"
}

func quoteOrEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return fmt.Sprintf("%q", s)
}
