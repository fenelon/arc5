package telegram

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"
)

// peerSourceID names a chat or sender by its Telegram type and ID: "user:1",
// "chat:2" for a basic group, "channel:3" for a supergroup or channel. The
// type matters because a basic group's message IDs are numbered per account
// and a supergroup's per channel (see the deletions in live.go).
func peerSourceID(p tg.PeerClass) string {
	switch p := p.(type) {
	case *tg.PeerUser:
		return fmt.Sprintf("user:%d", p.UserID)
	case *tg.PeerChat:
		return fmt.Sprintf("chat:%d", p.ChatID)
	case *tg.PeerChannel:
		return fmt.Sprintf("channel:%d", p.ChannelID)
	}
	return ""
}

// sourcePeer reads a source ID made by peerSourceID.
func sourcePeer(id string) (tg.PeerClass, bool) {
	kind, number, found := strings.Cut(id, ":")
	n, err := strconv.ParseInt(number, 10, 64)
	if !found || err != nil || n <= 0 || strconv.FormatInt(n, 10) != number {
		return nil, false
	}
	switch kind {
	case "user":
		return &tg.PeerUser{UserID: n}, true
	case "chat":
		return &tg.PeerChat{ChatID: n}, true
	case "channel":
		return &tg.PeerChannel{ChannelID: n}, true
	}
	return nil, false
}

// sharedNumbering reports whether a chat's message IDs are numbered across
// the whole account (private chats and basic groups), so that a deletion
// Telegram reports without a chat can be matched to it.
func sharedNumbering(sourceID string) bool {
	return strings.HasPrefix(sourceID, "user:") || strings.HasPrefix(sourceID, "chat:")
}
