package buildinfo_test

import (
	"runtime"
	"testing"

	"github.com/gamijoam/inventario-go/internal/platform/buildinfo"
)

func TestGetReportsRuntimeAndInjectedValues(t *testing.T) {
	saved := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = saved })
	buildinfo.Version = "v9.9.9"

	info := buildinfo.Get()
	if info.Version != "v9.9.9" {
		t.Errorf("Version = %q", info.Version)
	}
	if info.GoVersion != runtime.Version() || info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Errorf("datos de plataforma incorrectos: %+v", info)
	}
}
