package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/gotd/td/tg"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

const catchUpQueue = 10000

// Handle receives live updates from gotd's update manager: new messages,
// edits and deletions, and new names. A storage failure stops the run and
// holds the update position, so the update is asked for again; anything
// else wrong with one update skips that update.
func (r *run) Handle(ctx context.Context, u tg.UpdatesClass) error {
	var (
		list  []tg.UpdateClass
		users []tg.UserClass
		chats []tg.ChatClass
	)
	switch u := u.(type) {
	case *tg.Updates:
		list, users, chats = u.Updates, u.Users, u.Chats
	case *tg.UpdatesCombined:
		list, users, chats = u.Updates, u.Users, u.Chats
	case *tg.UpdateShort:
		list = []tg.UpdateClass{u.Update}
	default:
		return nil
	}
	r.learn(users, chats)
	if err := r.refreshChats(users, chats); err != nil {
		return r.storageFailed(err)
	}
	for _, update := range list {
		if err := r.handle(update); err != nil && errors.Is(err, connector.ErrStorage) {
			return r.storageFailed(err)
		}
	}
	return nil
}

func (r *run) storageFailed(err error) error {
	if !errors.Is(err, connector.ErrStorage) {
		return nil
	}
	r.hashes.hold(err)
	r.abort(err)
	return err
}

func (r *run) handle(update tg.UpdateClass) error {
	switch u := update.(type) {
	case *tg.UpdateNewMessage:
		return r.liveMessage(u.Message)
	case *tg.UpdateNewChannelMessage:
		return r.liveMessage(u.Message)
	case *tg.UpdateEditMessage:
		return r.liveMessage(u.Message)
	case *tg.UpdateEditChannelMessage:
		return r.liveMessage(u.Message)
	case *tg.UpdateDeleteMessages:
		return r.deleteShared(u.Messages)
	case *tg.UpdateDeleteChannelMessages:
		return r.sink.MarkDeleted(fmt.Sprintf("channel:%d", u.ChannelID), ids(u.Messages))
	}
	return nil
}

func ids(numbers []int) []string {
	out := make([]string, len(numbers))
	for i, n := range numbers {
		out[i] = strconv.Itoa(n)
	}
	return out
}

// liveMessage stores a new or edited message, adding its chat if it is new.
func (r *run) liveMessage(m tg.MessageClass) error {
	peer := messagePeer(m)
	if peer == nil {
		return nil
	}
	r.mu.Lock()
	chat, ok := r.e.chat(peer, r.selfID)
	r.mu.Unlock()
	if !ok {
		return nil
	}
	if err := r.ensureChat(chat); err != nil {
		return err
	}
	if archived, err := r.sink.ChatArchived(chat.SourceID); err != nil || !archived {
		return err
	}
	return r.store(m, true)
}

// ensureChat stores a chat unless this run already stored it as it is.
func (r *run) ensureChat(chat model.Chat) error {
	r.mu.Lock()
	same := r.known[chat.SourceID] == chat
	r.mu.Unlock()
	if same {
		return nil
	}
	if err := r.sink.UpsertChat(chat); err != nil {
		return err
	}
	r.mu.Lock()
	if r.known == nil {
		r.known = map[string]model.Chat{}
	}
	r.known[chat.SourceID] = chat
	r.mu.Unlock()
	return nil
}

// refreshChats renames chats already stored whose name an update changed.
func (r *run) refreshChats(users []tg.UserClass, chats []tg.ChatClass) error {
	var peers []tg.PeerClass
	for _, u := range users {
		if u, ok := u.(*tg.User); ok {
			peers = append(peers, &tg.PeerUser{UserID: u.ID})
		}
	}
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Chat:
			peers = append(peers, &tg.PeerChat{ChatID: c.ID})
		case *tg.Channel:
			peers = append(peers, &tg.PeerChannel{ChannelID: c.ID})
		}
	}
	for _, p := range peers {
		r.mu.Lock()
		old, stored := r.known[peerSourceID(p)]
		chat, ok := r.e.chat(p, r.selfID)
		r.mu.Unlock()
		if stored && ok && chat != old {
			if err := r.ensureChat(chat); err != nil {
				return err
			}
		}
	}
	return nil
}

// deleteShared marks messages deleted that Telegram named without a chat.
// Such IDs are numbered across private chats and basic groups only, so a
// supergroup or channel message with the same number is left alone.
func (r *run) deleteShared(numbers []int) error {
	for _, id := range ids(numbers) {
		chats, err := r.sink.LookupChats(id, []model.ChatKind{model.ChatPrivate, model.ChatBot, model.ChatGroup})
		if err != nil {
			return err
		}
		for _, chat := range chats {
			if !sharedNumbering(chat) {
				continue
			}
			if err := r.sink.MarkDeleted(chat, []string{id}); err != nil {
				return err
			}
		}
	}
	return nil
}

// tooLong is called when Telegram cannot replay everything missed while
// disconnected: every archived chat is brought up to date from its newest
// stored message.
func (r *run) tooLong() {
	r.mu.Lock()
	order := r.order
	r.mu.Unlock()
	go func() {
		for _, chat := range order {
			select {
			case r.catchUps <- chat:
			case <-r.ctx.Done():
				return
			}
		}
	}()
}

// channelTooLong is tooLong for one supergroup or channel.
func (r *run) channelTooLong(channelID int64) {
	select {
	case r.catchUps <- fmt.Sprintf("channel:%d", channelID):
	case <-r.ctx.Done():
	}
}

func (r *run) catchUpWorker(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case chat := <-r.catchUps:
			if err := r.catchUp(ctx, chat); err != nil && (errors.Is(err, connector.ErrStorage) || unauthorized(err)) {
				return err
			}
		}
	}
}

// catchUp fetches a chat's messages newer than its newest stored one.
func (r *run) catchUp(ctx context.Context, chat string) error {
	if archived, err := r.sink.ChatArchived(chat); err != nil || !archived {
		return err
	}
	top, err := loadTop(r.state, chat)
	if err != nil {
		return err
	}
	peer, ok := r.inputPeer(chat)
	if !ok {
		return nil
	}
	offset := 0
	for {
		var page historyPage
		if err := r.call(ctx, func() error {
			var err error
			page, err = r.conn.History(ctx, peer, offset, top, historyPageSize)
			return err
		}); err != nil {
			return err
		}
		if len(page.Messages) == 0 {
			return nil
		}
		r.learn(page.Users, page.Chats)
		for _, m := range page.Messages {
			if err := r.store(m, true); err != nil {
				return err
			}
			if id := m.GetID(); offset == 0 || id < offset {
				offset = id
			}
		}
	}
}
