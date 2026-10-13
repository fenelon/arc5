package telegram

import (
	"context"

	"github.com/gotd/td/tg"
)

// maxDialogPages bounds the chat list, in case Telegram keeps answering.
const maxDialogPages = 1000

// learn takes in users and chats from any response: their names for
// mapping, and their access hashes for requests, which are kept so a
// restart can name a chat before the chat list is read again.
func (r *run) learn(users []tg.UserClass, chats []tg.ChatClass, extra ...*tg.User) {
	for _, u := range extra {
		users = append(users, u)
	}
	r.mu.Lock()
	r.e.add(users, chats)
	r.mu.Unlock()
	ctx := context.Background()
	for _, u := range users {
		if u, ok := u.(*tg.User); ok && u.AccessHash != 0 && !u.Min {
			if h, found, _ := r.hashes.GetUserAccessHash(ctx, r.selfID, u.ID); !found || h != u.AccessHash {
				_ = r.hashes.SetUserAccessHash(ctx, r.selfID, u.ID, u.AccessHash)
			}
		}
	}
	for _, c := range chats {
		if c, ok := c.(*tg.Channel); ok && c.AccessHash != 0 && !c.Min {
			if h, found, _ := r.hashes.GetChannelAccessHash(ctx, r.selfID, c.ID); !found || h != c.AccessHash {
				_ = r.hashes.SetChannelAccessHash(ctx, r.selfID, c.ID, c.AccessHash)
			}
		}
	}
}

// syncChats reads the whole chat list and records every chat, so the
// Sources page can offer each one for archiving. The list comes most
// recently active first, which is the order history is fetched in.
func (r *run) syncChats(ctx context.Context) error {
	var (
		offset dialogOffset
		order  []string
		seen   = map[string]bool{}
	)
	for range maxDialogPages {
		page, err := r.conn.Dialogs(ctx, offset)
		if err != nil {
			return err
		}
		r.learn(page.Users, page.Chats)
		for _, d := range page.Dialogs {
			r.mu.Lock()
			chat, ok := r.e.chat(d.Peer, r.selfID)
			r.mu.Unlock()
			if !ok || seen[chat.SourceID] {
				continue
			}
			if err := r.ensureChat(chat); err != nil {
				return err
			}
			seen[chat.SourceID] = true
			order = append(order, chat.SourceID)
		}
		if page.Next == nil || len(page.Dialogs) == 0 || (page.Next.ID == offset.ID && page.Next.Date == offset.Date) {
			break
		}
		offset = *page.Next
	}
	r.mu.Lock()
	r.order = order
	r.mu.Unlock()
	return nil
}

// inputPeer names a chat for a request, from what this run has seen or
// from the access hashes kept by earlier runs.
func (r *run) inputPeer(sourceID string) (tg.InputPeerClass, bool) {
	peer, ok := sourcePeer(sourceID)
	if !ok {
		return nil, false
	}
	r.mu.Lock()
	in, known := r.e.inputPeer(peer)
	r.mu.Unlock()
	if known {
		return in, true
	}
	ctx := context.Background()
	switch p := peer.(type) {
	case *tg.PeerUser:
		if h, found, _ := r.hashes.GetUserAccessHash(ctx, r.selfID, p.UserID); found {
			return &tg.InputPeerUser{UserID: p.UserID, AccessHash: h}, true
		}
	case *tg.PeerChannel:
		if h, found, _ := r.hashes.GetChannelAccessHash(ctx, r.selfID, p.ChannelID); found {
			return &tg.InputPeerChannel{ChannelID: p.ChannelID, AccessHash: h}, true
		}
	}
	return nil, false
}
