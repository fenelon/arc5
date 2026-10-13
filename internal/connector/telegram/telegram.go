// Package telegram archives a Telegram account: its chats' history, new
// messages, edits and deletions as they happen, media, and pictures. It
// signs in as the owner's user account with the owner's own API keys.
package telegram

import "time"

// Connector is the Telegram connector.
type Connector struct {
	dial        dialer
	now         func() time.Time
	signOutWait time.Duration // how long signing out waits for Telegram
}

// New returns the connector, talking to the real Telegram.
func New() *Connector {
	return &Connector{dial: dialTelegram, now: time.Now, signOutWait: 20 * time.Second}
}
