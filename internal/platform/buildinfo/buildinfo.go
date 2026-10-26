// Package buildinfo expone la versión del ejecutable.
//
// Version, Commit y Date se inyectan al compilar:
//
//	go build -ldflags "-X github.com/gamijoam/inventario-go/internal/platform/buildinfo.Version=v0.1.0" ./cmd/balanzapro
//
// Si no se inyectan, Commit y Date se toman de la información VCS que Go
// incrusta al compilar dentro de un repositorio Git.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var (
	Version = "0.0.0-dev"
	Commit  = ""
	Date    = ""
)

// Info describe el ejecutable en ejecución.
type Info struct {
	Version   string
	Commit    string
	Date      string
	Modified  bool
	GoVersion string
	OS        string
	Arch      string
}

// Get devuelve la información de compilación del proceso actual.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			case "vcs.modified":
				info.Modified = s.Value == "true"
			}
		}
	}
	return info
}
