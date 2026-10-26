package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence"
	"github.com/gamijoam/inventario-go/internal/application/admin"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

const dbTimeout = 30 * time.Second

type migrationEntry struct {
	Version   int    `json:"version"`
	Name      string `json:"name"`
	AppliedAt string `json:"applied_at,omitempty"`
}

type dbStatusPayload struct {
	Schema         string           `json:"schema"`
	Engine         string           `json:"engine"`
	Location       string           `json:"location"`
	Initialized    bool             `json:"initialized"`
	Server         string           `json:"server"`
	InstallationID string           `json:"installation_id"`
	Applied        []migrationEntry `json:"applied"`
	Pending        []migrationEntry `json:"pending"`
}

type dbMigratePayload struct {
	Schema         string           `json:"schema"`
	Engine         string           `json:"engine"`
	Location       string           `json:"location"`
	Backup         string           `json:"backup"`
	Applied        []migrationEntry `json:"applied"`
	InstallationID string           `json:"installation_id"`
	Created        bool             `json:"installation_created"`
}

func newDBCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Administra la base de datos local (SQLite o PostgreSQL)",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Muestra motor, conexión y migraciones aplicadas o pendientes",
		Long:  "No modifica la base. Código de salida 5 si no se puede conectar.",
		Args:  noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDBStatus(cmd, env, flags) },
	}, &cobra.Command{
		Use:   "migrate",
		Short: "Crea la base si hace falta y aplica las migraciones pendientes",
		Long: "Aplica las migraciones pendientes, cada una en su transacción, y registra\n" +
			"la identidad de la instalación. En SQLite, si la base ya tenía\n" +
			"migraciones, primero crea un respaldo junto al archivo. En PostgreSQL,\n" +
			"respalde antes con las herramientas del servidor (pg_dump).",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runDBMigrate(cmd, env, flags) },
	})
	return cmd
}

func runDBStatus(cmd *cobra.Command, env Env, flags *globalFlags) error {
	rt, err := loadRuntime(env, flags, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	defer rt.close()
	ctx, cancel := context.WithTimeout(cmd.Context(), dbTimeout)
	defer cancel()

	dbCfg := rt.loaded.Config.Database
	p := dbStatusPayload{Schema: "balanzapro.db-status/v1", Engine: dbCfg.Driver, Location: location(dbCfg),
		Applied: []migrationEntry{}, Pending: []migrationEntry{}}

	store, err := persistence.Open(ctx, dbCfg, false)
	switch {
	case errors.Is(err, persistence.ErrNotInitialized):
		migs, lerr := (&persistence.Store{Engine: dbCfg.Driver}).Migrations()
		if lerr != nil {
			return lerr
		}
		for _, m := range migs {
			p.Pending = append(p.Pending, migrationEntry{Version: m.Version, Name: m.Name})
		}
		return printDBStatus(cmd.OutOrStdout(), flags, p)
	case err != nil:
		return unavailableError(err)
	}
	defer store.Close()
	p.Initialized = true
	if p.Server, err = store.Info(ctx); err != nil {
		return unavailableError(err)
	}
	runner, err := store.Runner()
	if err != nil {
		return err
	}
	st, err := runner.Status(ctx)
	if err != nil {
		return unavailableError(err)
	}
	for _, a := range st.Applied {
		p.Applied = append(p.Applied, migrationEntry{Version: a.Version, Name: a.Name, AppliedAt: a.AppliedAt})
	}
	for _, m := range st.Pending {
		p.Pending = append(p.Pending, migrationEntry{Version: m.Version, Name: m.Name})
	}
	if len(st.Applied) > 0 && len(st.Pending) == 0 {
		if inst, err := store.Installation(ctx); err == nil {
			p.InstallationID = inst.ID.String()
		}
	}
	if err := printDBStatus(cmd.OutOrStdout(), flags, p); err != nil {
		return err
	}
	return st.Problem()
}

func printDBStatus(w io.Writer, flags *globalFlags, p dbStatusPayload) error {
	if flags.json {
		return writeJSON(w, p)
	}
	fmt.Fprintf(w, "motor:        %s\nubicación:    %s\n", p.Engine, p.Location)
	if !p.Initialized {
		fmt.Fprintf(w, "estado:       no inicializada; ejecute 'balanzapro db migrate'\n")
	} else {
		fmt.Fprintf(w, "servidor:     %s\ninstalación:  %s\n", p.Server, orUnknown(p.InstallationID))
	}
	fmt.Fprintf(w, "migraciones:  %d aplicadas, %d pendientes\n", len(p.Applied), len(p.Pending))
	for _, m := range p.Applied {
		fmt.Fprintf(w, "  [aplicada]  %04d_%s  %s\n", m.Version, m.Name, caracas(m.AppliedAt))
	}
	for _, m := range p.Pending {
		fmt.Fprintf(w, "  [pendiente] %04d_%s\n", m.Version, m.Name)
	}
	return nil
}

func runDBMigrate(cmd *cobra.Command, env Env, flags *globalFlags) error {
	rt, err := loadRuntime(env, flags, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	defer rt.close()
	ctx, cancel := context.WithTimeout(cmd.Context(), dbTimeout)
	defer cancel()

	dbCfg := rt.loaded.Config.Database
	store, err := persistence.Open(ctx, dbCfg, true)
	if err != nil {
		return unavailableError(err)
	}
	defer store.Close()
	runner, err := store.Runner()
	if err != nil {
		return err
	}
	st, err := runner.Status(ctx)
	if err != nil {
		return unavailableError(err)
	}
	if err := st.Problem(); err != nil {
		return err
	}

	p := dbMigratePayload{Schema: "balanzapro.db-migrate/v1", Engine: store.Engine, Location: store.Location, Applied: []migrationEntry{}}
	// Respaldo previo (sección 28.10) cuando hay datos que proteger.
	if store.Engine == config.DriverSQLite && len(st.Applied) > 0 && len(st.Pending) > 0 {
		p.Backup = fmt.Sprintf("%s.pre-%04d-%s.bak", dbCfg.SQLite.Path, st.Pending[0].Version, time.Now().UTC().Format("20060102T150405Z"))
		if err := store.BackupSQLite(ctx, p.Backup); err != nil {
			return err
		}
		rt.logger.Info("respaldo previo a la migración", "path", p.Backup)
	}

	done, err := runner.Up(ctx)
	for _, m := range done {
		p.Applied = append(p.Applied, migrationEntry{Version: m.Version, Name: m.Name})
		rt.logger.Info("migración aplicada", "version", m.Version, "name", m.Name, "engine", store.Engine)
	}
	if err != nil {
		return err
	}
	inst, created, err := store.EnsureInstallation(ctx)
	if err != nil {
		return err
	}
	p.InstallationID, p.Created = inst.ID.String(), created

	if flags.json {
		return writeJSON(cmd.OutOrStdout(), p)
	}
	out := cmd.OutOrStdout()
	if p.Backup != "" {
		fmt.Fprintf(out, "respaldo previo: %s\n", p.Backup)
	}
	if len(done) == 0 {
		fmt.Fprintln(out, "la base ya estaba al día")
	}
	for _, m := range done {
		fmt.Fprintf(out, "aplicada %04d_%s\n", m.Version, m.Name)
	}
	verb := "existente"
	if created {
		verb = "registrada"
	}
	fmt.Fprintf(out, "instalación %s: %s (%s)\n", verb, p.InstallationID, store.Engine)
	return nil
}

// databaseCheck verifica conexión y esquema sin crear ni modificar nada.
func databaseCheck(env Env, flags *globalFlags) admin.Check {
	return admin.Check{
		Name: "database",
		Run: func(ctx context.Context) (admin.Status, string) {
			loaded, err := config.Load(env.ConfigOptions(flags.configPath))
			if err != nil {
				return admin.StatusFail, "configuración inválida; ver la verificación config"
			}
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			store, err := persistence.Open(ctx, loaded.Config.Database, false)
			if errors.Is(err, persistence.ErrNotInitialized) {
				return admin.StatusWarn, "base no inicializada; ejecute 'balanzapro db migrate'"
			}
			if err != nil {
				return admin.StatusFail, err.Error()
			}
			defer store.Close()
			info, err := store.Info(ctx)
			if err != nil {
				return admin.StatusFail, err.Error()
			}
			runner, err := store.Runner()
			if err != nil {
				return admin.StatusFail, err.Error()
			}
			st, err := runner.Status(ctx)
			if err != nil {
				return admin.StatusFail, err.Error()
			}
			if err := st.Problem(); err != nil {
				return admin.StatusFail, info + "; " + err.Error()
			}
			if len(st.Pending) > 0 {
				return admin.StatusWarn, fmt.Sprintf("%s; %d migraciones pendientes: ejecute 'balanzapro db migrate'", info, len(st.Pending))
			}
			detail := fmt.Sprintf("%s; esquema al día (%d migraciones)", info, len(st.Applied))
			if _, err := store.Installation(ctx); errors.Is(err, sql.ErrNoRows) {
				return admin.StatusWarn, detail + "; instalación sin registrar: ejecute 'balanzapro db migrate'"
			}
			return admin.StatusOK, detail
		},
	}
}

func location(c config.Database) string {
	if c.Driver == config.DriverPostgres {
		return config.RedactURL(c.Postgres.URL.Reveal())
	}
	return c.SQLite.Path
}

// caracas presenta un instante RFC 3339 en hora de Caracas.
func caracas(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return timezone.InCaracas(t).Format("2006-01-02 15:04:05 (VET)")
}
