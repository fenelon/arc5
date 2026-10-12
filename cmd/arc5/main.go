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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/config"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/connector/demo"
	"github.com/fenelon/arc5/internal/search"
	"github.com/fenelon/arc5/internal/web"
)

const usage = `usage:
  arc5 serve   [--listen address] [--data folder]
  arc5 reindex [--data folder] [--languages en,ru,es]
  arc5 demo    [--data folder]`

// languagesKey is the setting that holds the languages search stems, as
// comma-separated codes.
const languagesKey = "search_languages"

// The demo connector is registered once per process, so the sample archive's
// source shows as live.
func init() { demo.Register(connector.Default) }

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
	case "reindex":
		cfg, err := config.ParseReindex(args[1:], getenv)
		if err != nil {
			return fmt.Errorf("%w\n%s", err, usage)
		}
		return reindex(ctx, cfg)
	case "demo":
		cfg, err := config.ParseDemo(args[1:], getenv)
		if err != nil {
			return fmt.Errorf("%w\n%s", err, usage)
		}
		return seedDemo(cfg)
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

// loadAnalyzer builds the search analyzer for the languages in settings.
func loadAnalyzer(store *archive.Store) (*search.Analyzer, error) {
	setting, ok, err := store.Setting(languagesKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		setting = search.DefaultLanguages
	}
	analyzer, err := search.NewAnalyzer(search.ParseLanguages(setting))
	if err != nil {
		return nil, fmt.Errorf("%s setting: %w", languagesKey, err)
	}
	return analyzer, nil
}

func serve(ctx context.Context, cfg config.Config, ready func(addr string)) error {
	store, err := archive.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	analyzer, err := loadAnalyzer(store)
	if err != nil {
		return err
	}
	store.SetAnalyzer(analyzer)
	current, err := store.IndexCurrent()
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	// Connectors and the index rebuild stop before the store closes,
	// whichever way serve returns.
	ctx, cancel := context.WithCancel(ctx)
	runner := connector.NewRunner(store, connector.Default)
	var background sync.WaitGroup
	defer func() {
		cancel()
		runner.Wait()
		background.Wait()
	}()

	if !current {
		background.Add(1)
		go func() {
			defer background.Done()
			log.Printf("rebuilding the search index for %s", analyzer.Signature())
			if err := store.Reindex(ctx, nil); err != nil {
				if ctx.Err() == nil {
					log.Printf("search index rebuild failed: %v", err)
				}
				return
			}
			log.Printf("search index rebuilt")
		}()
	}
	if err := runner.Start(ctx); err != nil {
		ln.Close()
		return err
	}

	site, err := web.New(web.Deps{Store: store, Runner: runner, Registry: connector.Default, EnvPassword: cfg.Password})
	if err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: site, ReadHeaderTimeout: 10 * time.Second}
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

// reindex rebuilds the search index, changing the language list first if one
// was given. It may run while a server has the same data folder open, but a
// running server keeps its old language list until it is restarted.
func reindex(ctx context.Context, cfg config.Reindex) error {
	store, err := archive.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	if cfg.Languages != "" {
		chosen, err := search.NewAnalyzer(search.ParseLanguages(cfg.Languages))
		if err != nil {
			return err
		}
		if err := store.SetSetting(languagesKey, strings.Join(chosen.Languages(), ",")); err != nil {
			return err
		}
	}
	analyzer, err := loadAnalyzer(store)
	if err != nil {
		return err
	}
	store.SetAnalyzer(analyzer)

	indexed := 0
	if err := store.Reindex(ctx, func(done, total int) { indexed = done }); err != nil {
		return err
	}
	languages := strings.Join(analyzer.Languages(), ", ")
	if languages == "" {
		languages = "none"
	}
	fmt.Printf("indexed %d messages; word forms for: %s\n", indexed, languages)
	return nil
}

// seedDemo writes the sample archive into an empty data folder.
func seedDemo(cfg config.Demo) error {
	store, err := archive.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	// With the analyzer set, the sample messages are indexed as they are written.
	analyzer, err := loadAnalyzer(store)
	if err != nil {
		return err
	}
	store.SetAnalyzer(analyzer)

	seeded, err := demo.Seed(store)
	if err != nil {
		return err
	}
	if !seeded {
		return fmt.Errorf("%s already has sources; the demo archive is only written into an empty data folder", cfg.DataDir)
	}
	fmt.Printf("demo archive written to %s; start it with: arc5 serve --data %s\n", cfg.DataDir, cfg.DataDir)
	return nil
}
