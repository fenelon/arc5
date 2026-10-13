package telegram

import (
	"context"

	"github.com/gotd/td/tg"
)

// Handle receives live updates. (Task 10 fills this in.)
func (r *run) Handle(ctx context.Context, u tg.UpdatesClass) error { return nil }

func (r *run) tooLong()             {}
func (r *run) channelTooLong(int64) {}
