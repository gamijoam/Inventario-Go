package cli

import (
	"io"
	"log/slog"

	"github.com/gamijoam/inventario-go/internal/platform/config"
	"github.com/gamijoam/inventario-go/internal/platform/logging"
)

// runtime agrupa la configuración cargada y el logger de un comando.
type runtime struct {
	loaded config.Loaded
	logger *slog.Logger
	close  func() error
}

// loadRuntime carga la configuración y crea el logger. --log-level
// prevalece sobre archivo y entorno. Sin archivo de log, los registros van a
// fallback; nil los descarta (TUI). Los logs nunca van a stdout.
func loadRuntime(env Env, flags *globalFlags, fallback io.Writer) (runtime, error) {
	loaded, err := config.Load(env.ConfigOptions(flags.configPath))
	if err != nil {
		return runtime{}, configError(err)
	}
	logCfg := loaded.Config.Log
	if flags.logLevel != "" {
		logCfg.Level = flags.logLevel
		if _, err := logging.New(io.Discard, logCfg); err != nil {
			return runtime{}, usageErrorf("--log-level: %s", err)
		}
	}
	if logCfg.File == "" && fallback == nil {
		return runtime{loaded: loaded, logger: logging.Discard(), close: func() error { return nil }}, nil
	}
	logger, closeFn, err := logging.Open(logCfg, fallback)
	if err != nil {
		return runtime{}, configError(err)
	}
	logger.Debug("configuración cargada", "path", loaded.Path, "file_found", loaded.FileFound,
		"driver", loaded.Config.Database.Driver)
	return runtime{loaded: loaded, logger: logger, close: closeFn}, nil
}
