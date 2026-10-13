// Package indexer keeps the search index in step with the languages the
// owner chose. It holds the analyzer the archive indexes with and the
// searcher that reads the index, swaps both when the languages change, and
// rebuilds the index in the background.
package indexer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/search"
)

// LanguagesKey is the setting that holds the languages search stems, as
// comma-separated codes.
const LanguagesKey = "search_languages"

// ErrUnknownLanguage is returned for a language stemming is not available for.
var ErrUnknownLanguage = errors.New("unknown search language")

// Status says which languages the index is for and how a rebuild is going.
type Status struct {
	Languages  []string // codes, sorted
	Rebuilding bool
	Done       int    // messages indexed by the current or last rebuild
	Total      int    // messages to index in it
	Err        string // why the last rebuild failed, if it did
}

type Indexer struct {
	store *archive.Store
	// Logf records rebuilds. Defaults to log.Printf.
	Logf func(format string, args ...any)

	setMu    sync.Mutex // one language change at a time
	mu       sync.Mutex
	searcher *search.Searcher
	status   Status
	ctx      context.Context    // set by Start; bounds every rebuild
	cancel   context.CancelFunc // stops the rebuild in flight
	done     chan struct{}      // closed when the rebuild in flight ends
}

// Load reads the language setting and makes the archive index with it.
func Load(store *archive.Store) (*Indexer, error) {
	setting, ok, err := store.Setting(LanguagesKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		setting = search.DefaultLanguages
	}
	analyzer, err := search.NewAnalyzer(search.ParseLanguages(setting))
	if err != nil {
		return nil, fmt.Errorf("%s setting: %w", LanguagesKey, err)
	}
	x := &Indexer{store: store, Logf: log.Printf, ctx: context.Background()}
	x.use(analyzer)
	return x, nil
}

// use makes analyzer the one the archive writes and the searcher reads with.
func (x *Indexer) use(analyzer *search.Analyzer) {
	x.store.SetAnalyzer(analyzer)
	x.mu.Lock()
	x.searcher = search.New(x.store.Reader(), analyzer)
	x.status.Languages = analyzer.Languages()
	x.mu.Unlock()
}

// Searcher returns the searcher for the languages now chosen.
func (x *Indexer) Searcher() *search.Searcher {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.searcher
}

func (x *Indexer) Status() Status {
	x.mu.Lock()
	defer x.mu.Unlock()
	st := x.status
	st.Languages = append([]string(nil), st.Languages...)
	return st
}

// Start rebuilds the index in the background if it was built for other
// languages or written to by a process that indexes differently. Rebuilds
// stop when ctx is cancelled; call Wait afterwards.
func (x *Indexer) Start(ctx context.Context) error {
	x.mu.Lock()
	x.ctx = ctx
	x.mu.Unlock()
	current, err := x.store.IndexCurrent()
	if err != nil || current {
		return err
	}
	x.rebuild()
	return nil
}

// SetLanguages saves a new language list, indexes new messages with it at
// once, and rebuilds the rest of the index in the background. A rebuild
// already running is stopped first. Searches keep working meanwhile, though
// they find only what has been indexed again so far.
func (x *Indexer) SetLanguages(codes []string) error {
	analyzer, err := search.NewAnalyzer(codes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnknownLanguage, err)
	}
	x.setMu.Lock()
	defer x.setMu.Unlock()
	x.stop()
	if err := x.store.SetSetting(LanguagesKey, strings.Join(analyzer.Languages(), ",")); err != nil {
		return err
	}
	x.use(analyzer)
	x.rebuild()
	return nil
}

// stop cancels the rebuild in flight and waits for it to end.
func (x *Indexer) stop() {
	x.mu.Lock()
	cancel, done := x.cancel, x.done
	x.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// rebuild starts a background rebuild with the analyzer now set.
func (x *Indexer) rebuild() {
	x.mu.Lock()
	ctx, cancel := context.WithCancel(x.ctx)
	done := make(chan struct{})
	x.cancel, x.done = cancel, done
	x.status.Rebuilding, x.status.Done, x.status.Total, x.status.Err = true, 0, 0, ""
	languages := strings.Join(x.status.Languages, ",")
	x.mu.Unlock()

	go func() {
		defer close(done)
		x.Logf("rebuilding the search index for languages %q", languages)
		err := x.store.Reindex(ctx, func(n, total int) {
			x.mu.Lock()
			x.status.Done, x.status.Total = n, total
			x.mu.Unlock()
		})
		x.mu.Lock()
		defer x.mu.Unlock()
		x.status.Rebuilding = false
		switch {
		case err == nil:
			x.Logf("search index rebuilt")
		case ctx.Err() != nil:
			// Stopped for a newer rebuild or for shutdown.
		default:
			x.status.Err = err.Error()
			x.Logf("search index rebuild failed: %v", err)
		}
	}()
}

// Wait blocks until the rebuild in flight, if any, has ended.
func (x *Indexer) Wait() {
	x.mu.Lock()
	done := x.done
	x.mu.Unlock()
	if done != nil {
		<-done
	}
}
