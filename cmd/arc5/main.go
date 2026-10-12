// Command arc5 is a self-hosted message archive.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/config"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/web"
)

const usage = "usage: arc5 serve [--listen address] [--data folder]"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, nil); err != nil {
		fmt.Fprintln(os.Stderr, "arc5:", err)
		os.Exit(1)
	}
}

// run executes one command. ready, if set, is called with the listening
// address once the server accepts connections.
func run(ctx context.Context, args []string, getenv func(string) string, ready func(addr string)) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "serve":
		cfg, err := config.Parse(args[1:], getenv)
		if err != nil {
			return fmt.Errorf("%w\n%s", err, usage)
		}
		return serve(ctx, cfg, ready)
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func serve(ctx context.Context, cfg config.Config, ready func(addr string)) error {
	store, err := archive.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	// Connectors stop before the store closes, whichever way serve returns.
	ctx, cancel := context.WithCancel(ctx)
	runner := connector.NewRunner(store, connector.Default)
	defer func() {
		cancel()
		runner.Wait()
	}()
	if err := runner.Start(ctx); err != nil {
		ln.Close()
		return err
	}

	srv := &http.Server{Handler: web.NewHandler(), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	log.Printf("arc5 listening on http://%s, data in %s", ln.Addr(), cfg.DataDir)
	if ready != nil {
		ready(ln.Addr().String())
	}

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	return srv.Shutdown(shutdownCtx)
}
