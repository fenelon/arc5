package telegram

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

const historyPageSize = 100

// backfill fetches each archived chat's history, most recently active chat
// first, backwards from its checkpoint to the age limit. The checkpoint is
// written after a page is stored, never before.
func (r *run) backfill(ctx context.Context) error {
	if r.opts.paused {
		r.sink.ReportProgress(connector.Progress{Phase: connector.PhaseLive, Detail: "catch-up paused"})
		return nil
	}
	r.mu.Lock()
	order := r.order
	r.mu.Unlock()
	var chats []string
	for _, chat := range order {
		archived, err := r.sink.ChatArchived(chat)
		if err != nil {
			return err
		}
		if archived {
			chats = append(chats, chat)
		}
	}
	for done, chat := range chats {
		r.sink.ReportProgress(connector.Progress{Phase: connector.PhaseBackfill, ChatsDone: done, ChatsTotal: len(chats)})
		if err := r.backfillChat(ctx, chat); err != nil && !refusedChat(err) {
			return err // a chat Telegram refuses is skipped; the others carry on
		}
	}
	r.sink.ReportProgress(connector.Progress{Phase: connector.PhaseLive, ChatsDone: len(chats), ChatsTotal: len(chats)})
	return nil
}

func (r *run) backfillChat(ctx context.Context, chat string) error {
	cp, err := loadCheckpoint(r.state, chat)
	if err != nil {
		return err
	}
	limit := r.opts.oldest
	if cp.Start || (cp.Limit != 0 && !limit.IsZero() && limit.Unix() >= cp.Limit) {
		return nil // the start of the chat, or this age limit, was reached before
	}
	peer, ok := r.inputPeer(chat)
	if !ok {
		return nil // Telegram has not told this run how to name the chat
	}
	for {
		if err := r.pace(ctx); err != nil {
			return err
		}
		var page historyPage
		err := r.call(ctx, func() error {
			var err error
			page, err = r.conn.History(ctx, peer, cp.Oldest, 0, historyPageSize)
			return err
		})
		if err != nil {
			return err
		}
		if len(page.Messages) == 0 {
			cp.Start = true
			return saveCheckpoint(r.state, chat, cp)
		}
		r.learn(page.Users, page.Chats)
		reached := false
		for _, m := range page.Messages {
			date := messageDate(m)
			if !limit.IsZero() && date < limit.Unix() {
				reached = true
				continue
			}
			if err := r.store(m, false); err != nil {
				return err
			}
			if id := m.GetID(); cp.Oldest == 0 || id < cp.Oldest {
				cp.Oldest, cp.OldestDate = id, date
			}
		}
		if reached {
			cp.Limit = limit.Unix()
			return saveCheckpoint(r.state, chat, cp)
		}
		if err := saveCheckpoint(r.state, chat, cp); err != nil {
			return err
		}
	}
}

// pace spaces history requests by the speed setting: every request after
// the first of a run waits its turn.
func (r *run) pace(ctx context.Context) error {
	r.mu.Lock()
	first := !r.paced
	r.paced = true
	r.mu.Unlock()
	if first {
		return nil
	}
	return r.c.sleep(ctx, r.opts.pause)
}

func messageDate(m tg.MessageClass) int64 {
	if d, ok := m.(interface{ GetDate() int }); ok {
		return int64(d.GetDate())
	}
	return 0
}

// call makes a request, waiting out Telegram's flood waits in full and
// trying again; any other error is returned.
func (r *run) call(ctx context.Context, request func() error) error {
	for {
		err := request()
		wait, flooded := floodWait(err)
		if !flooded {
			return err
		}
		until := r.c.now().Add(wait).Local().Format("15:04:05")
		r.sink.ReportProgress(connector.Progress{Phase: connector.PhaseRateLimited, Detail: fmt.Sprintf("until %s", until)})
		if err := r.c.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// store maps a message and stores it, with its sender, and queues its media.
func (r *run) store(m tg.MessageClass, live bool) error {
	r.mu.Lock()
	msg, media, ok := mapMessage(m, r.e, r.opts.keepEphemeral)
	var sender senderUpdate
	if ok && msg.SenderSourceID != "" {
		if from, known := sourcePeer(msg.SenderSourceID); known {
			sender.s, sender.ok = r.e.sender(from)
		}
	}
	r.mu.Unlock()
	if !ok {
		return nil
	}
	if sender.ok {
		if err := r.upsertSender(sender.s); err != nil {
			return err
		}
	} else {
		msg.SenderSourceID = "" // a sender Telegram did not describe cannot be stored
	}
	refs, err := r.sink.UpsertMessage(msg)
	if err != nil {
		return err
	}
	for i := range media {
		media[i].live = live
	}
	r.queueMedia(msg.ChatSourceID, msg.SourceID, refs, media, live)
	return raiseTop(r.state, msg.ChatSourceID, m.GetID())
}

type senderUpdate struct {
	s  model.Sender
	ok bool
}

// upsertSender stores a sender unless this run already stored it as it is.
func (r *run) upsertSender(s model.Sender) error {
	r.mu.Lock()
	if r.senders == nil {
		r.senders = map[string]model.Sender{}
	}
	same := r.senders[s.SourceID] == s
	r.mu.Unlock()
	if same {
		return nil
	}
	if err := r.sink.UpsertSender(s); err != nil {
		return err
	}
	r.mu.Lock()
	r.senders[s.SourceID] = s
	r.mu.Unlock()
	return r.queueAvatar(model.AvatarSender, s.SourceID)
}
