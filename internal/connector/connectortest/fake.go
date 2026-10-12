// Package connectortest provides a scripted connector for exercising the
// ingest path without a real message source.
package connectortest

import (
	"bytes"
	"context"
	"sync"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

// Step is one thing the fake connector does to its sink.
type Step func(sink connector.Sink) error

// Fake applies its steps in order each time it is run, then idles until cancelled.
type Fake struct {
	steps []Step
	once  sync.Once
	done  chan struct{}
	err   error
}

func New(steps ...Step) *Fake {
	return &Fake{steps: steps, done: make(chan struct{})}
}

// Done is closed when the first run has applied every step or hit an error.
func (f *Fake) Done() <-chan struct{} { return f.done }

// Err is the first run's step error, if any. Read it only after Done is closed.
func (f *Fake) Err() error { return f.err }

func (f *Fake) Run(ctx context.Context, sink connector.Sink, _ connector.State, _ map[string]string) error {
	var err error
	for _, step := range f.steps {
		if err = step(sink); err != nil {
			break
		}
	}
	f.once.Do(func() {
		f.err = err
		close(f.done)
	})
	<-ctx.Done()
	return ctx.Err()
}

func (f *Fake) Setup(connector.State) connector.SetupFlow { return nil }
func (f *Fake) Settings() []connector.Field               { return nil }

func Chat(c model.Chat) Step {
	return func(sink connector.Sink) error { return sink.UpsertChat(c) }
}

func Sender(s model.Sender) Step {
	return func(sink connector.Sink) error { return sink.UpsertSender(s) }
}

func Message(m model.Message) Step {
	return func(sink connector.Sink) error {
		_, err := sink.UpsertMessage(m)
		return err
	}
}

// Delete reports a deletion the way a source that names the chat does.
func Delete(chatSourceID string, ids ...string) Step {
	return func(sink connector.Sink) error { return sink.MarkDeleted(chatSourceID, ids) }
}

// DeleteWithoutChat reports a deletion the way a source that gives only
// message IDs does: the connector looks the chat up first.
func DeleteWithoutChat(kinds []model.ChatKind, ids ...string) Step {
	return func(sink connector.Sink) error {
		for _, id := range ids {
			chats, err := sink.LookupChats(id, kinds)
			if err != nil {
				return err
			}
			for _, chat := range chats {
				if err := sink.MarkDeleted(chat, []string{id}); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func Media(ref model.AttachmentRef, content []byte) Step {
	return func(sink connector.Sink) error { return sink.PutMedia(ref, bytes.NewReader(content)) }
}
