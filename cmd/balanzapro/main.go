// Comando balanzapro: CLI y TUI de BalanzaPro.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/term"

	"github.com/gamijoam/inventario-go/internal/adapters/cli"
	"github.com/gamijoam/inventario-go/internal/adapters/tui"
	"github.com/gamijoam/inventario-go/internal/platform/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Execute(ctx, os.Args[1:], cli.Env{
		In:            os.Stdin,
		Out:           os.Stdout,
		Err:           os.Stderr,
		Getenv:        os.Getenv,
		Interactive:   term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		RunTUI:        tui.Run,
		ConfigOptions: config.SystemOptions,
		ReadPassword:  readPassword,
	})
	stop()
	os.Exit(code)
}

// readPassword muestra el aviso en stderr y lee de la terminal sin eco.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}
