package telegram

import (
	"context"

	"github.com/fenelon/arc5/internal/connector"
)

// backfill fetches history. (Task 9 fills this in.)
func (r *run) backfill(ctx context.Context) error {
	r.sink.ReportProgress(connector.Progress{Phase: connector.PhaseLive})
	return nil
}
