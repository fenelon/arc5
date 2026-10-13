package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

// newTestRun builds a run as Run would, without connecting.
func newTestRun(t *testing.T, fc *fakeConn, sink *memSink) (*run, *sleeps) {
	t.Helper()
	sl := &sleeps{}
	c := newTestConnector(&fakeDialer{conn: fc})
	c.sleep = sl.sleep
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	state := signedIn(t)
	r := &run{c: c, sink: sink, state: state, opts: readOptions(nil, time.Now), hashes: newUpdateStorage(state),
		e: fixture(), ctx: ctx, abort: cancel, catchUps: make(chan string, 10), conn: fc, selfID: self}
	for _, chat := range []model.Chat{{SourceID: "user:1", Kind: model.ChatPrivate, Title: "Dan"}, {SourceID: "chat:10", Kind: model.ChatGroup, Title: "Familia"}} {
		if err := sink.UpsertChat(chat); err != nil {
			t.Fatal(err)
		}
	}
	return r, sl
}

// photoMessage is message id in Dan's chat with photo number photo.
func photoMessage(id int, photo int64, ref byte) *tg.Message {
	return &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: 1}, Date: int(day0.Unix()),
		Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: photo, FileReference: []byte{ref}, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "y", Size: 10}}}}}
}

// downloads records which files were fetched, by photo ID and reference.
type downloads struct {
	mu   sync.Mutex
	got  []string
	fail func(loc tg.InputFileLocationClass) error
}

func (d *downloads) download(loc tg.InputFileLocationClass, w io.Writer) error {
	name := fmt.Sprintf("%T", loc)
	switch l := loc.(type) {
	case *tg.InputPhotoFileLocation:
		name = fmt.Sprintf("photo:%d@%d", l.ID, l.FileReference[0])
	case *tg.InputPeerPhotoFileLocation:
		name = fmt.Sprintf("avatar:%d", l.PhotoID)
	}
	d.mu.Lock()
	d.got = append(d.got, name)
	fail := d.fail
	d.mu.Unlock()
	if fail != nil {
		if err := fail(loc); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "bytes of "+name)
	return err
}

func (d *downloads) list() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.got)
}

// files are the downloads of attachments, leaving out pictures.
func (d *downloads) files() []string {
	var out []string
	for _, name := range d.list() {
		if strings.HasPrefix(name, "photo:") {
			out = append(out, name)
		}
	}
	return out
}

// working runs the media worker until cond holds.
func working(t *testing.T, r *run, what string, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.mediaWorker(ctx) }()
	eventually(t, what, cond)
	cancel()
	if err := <-done; err != nil {
		t.Errorf("the media worker stopped with %v", err)
	}
}

func TestLiveMediaBeforeBackfill(t *testing.T) {
	dl := &downloads{}
	sink := newMemSink()
	r, sl := newTestRun(t, &fakeConn{download: dl.download}, sink)
	for id := 1; id <= 3; id++ {
		if err := r.store(photoMessage(id, int64(100+id), 1), false); err != nil {
			t.Fatal(err)
		}
	}
	for id := 4; id <= 5; id++ {
		if err := r.store(photoMessage(id, int64(100+id), 1), true); err != nil {
			t.Fatal(err)
		}
	}
	working(t, r, "five files", func() bool { return len(dl.files()) == 5 })
	if got := dl.files(); !slices.Equal(got[:2], []string{"photo:104@1", "photo:105@1"}) {
		t.Errorf("download order = %v, want live first", got)
	}
	if string(sink.media[model.AttachmentRef{ChatSourceID: "user:1", MessageSourceID: "4"}]) != "bytes of photo:104@1" {
		t.Errorf("media = %v", sink.media)
	}
	// Backfill downloads keep the speed setting; live ones do not wait.
	if n := len(sl.all()); n != 2 {
		t.Errorf("%d pauses for 3 backfill files after the first request", n)
	}
}

func TestMediaRetriesThenFails(t *testing.T) {
	dl := &downloads{fail: func(tg.InputFileLocationClass) error { return errors.New("connection reset") }}
	sink := newMemSink()
	r, sl := newTestRun(t, &fakeConn{download: dl.download}, sink)
	if err := r.store(photoMessage(1, 101, 1), true); err != nil {
		t.Fatal(err)
	}
	ref := model.AttachmentRef{ChatSourceID: "user:1", MessageSourceID: "1"}
	working(t, r, "the failure", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.statuses[ref] != "" })
	if got := dl.files(); len(got) != 3 {
		t.Errorf("tries = %v", got)
	}
	if !strings.HasPrefix(sink.statuses[ref], "failed: ") || !strings.Contains(sink.statuses[ref], "connection reset") {
		t.Errorf("status = %q", sink.statuses[ref])
	}
	if got := sl.all(); !slices.Equal(got, []time.Duration{time.Second, 2 * time.Second}) {
		t.Errorf("backoff = %v", got)
	}
}

func TestMediaTelegramNoLongerHas(t *testing.T) {
	dl := &downloads{fail: func(tg.InputFileLocationClass) error { return tgerr.New(400, "FILE_ID_INVALID") }}
	sink := newMemSink()
	r, _ := newTestRun(t, &fakeConn{download: dl.download}, sink)
	r.store(photoMessage(1, 101, 1), true)
	ref := model.AttachmentRef{ChatSourceID: "user:1", MessageSourceID: "1"}
	working(t, r, "the answer", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.statuses[ref] != "" })
	if !strings.HasPrefix(sink.statuses[ref], "unavailable: ") || len(dl.files()) != 1 {
		t.Errorf("status = %q after %v", sink.statuses[ref], dl.files())
	}
}

func TestMediaRefreshesAnExpiredReference(t *testing.T) {
	dl := &downloads{fail: func(loc tg.InputFileLocationClass) error {
		if l, ok := loc.(*tg.InputPhotoFileLocation); ok && l.FileReference[0] == 1 {
			return tgerr.New(400, "FILE_REFERENCE_EXPIRED")
		}
		return nil
	}}
	sink := newMemSink()
	fc := &fakeConn{download: dl.download, messages: func(peer tg.InputPeerClass, ids []int) (historyPage, error) {
		if inputSourceID(peer) != "user:1" || !slices.Equal(ids, []int{1}) {
			return historyPage{}, errors.New("unexpected")
		}
		return historyPage{Messages: []tg.MessageClass{photoMessage(1, 101, 2)}}, nil
	}}
	r, _ := newTestRun(t, fc, sink)
	r.store(photoMessage(1, 101, 1), true)
	ref := model.AttachmentRef{ChatSourceID: "user:1", MessageSourceID: "1"}
	working(t, r, "the file", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.media[ref] != nil })
	if got := dl.files(); !slices.Equal(got, []string{"photo:101@1", "photo:101@2"}) {
		t.Errorf("downloads = %v", got)
	}
}

func TestRequestedMedia(t *testing.T) {
	dl := &downloads{}
	sink := newMemSink()
	fc := &fakeConn{download: dl.download, messages: func(_ tg.InputPeerClass, ids []int) (historyPage, error) {
		return historyPage{Messages: []tg.MessageClass{photoMessage(ids[0], 777, 3)}}, nil
	}}
	r, _ := newTestRun(t, fc, sink)
	ref := model.AttachmentRef{ChatSourceID: "user:1", MessageSourceID: "9"}
	sink.requests <- ref
	working(t, r, "the requested file", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.media[ref] != nil })
	if got := dl.files(); !slices.Equal(got, []string{"photo:777@3"}) {
		t.Errorf("downloads = %v", got)
	}
	gone := model.AttachmentRef{ChatSourceID: "user:1", MessageSourceID: "10"}
	fc.messages = func(tg.InputPeerClass, []int) (historyPage, error) {
		return historyPage{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 10}}}, nil
	}
	sink.requests <- gone
	working(t, r, "the answer", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.statuses[gone] != "" })
	if !strings.HasPrefix(sink.statuses[gone], "unavailable: ") {
		t.Errorf("a message Telegram no longer has: %q", sink.statuses[gone])
	}
}

func TestAvatarsAreDownloadedOnce(t *testing.T) {
	dl := &downloads{}
	sink := newMemSink()
	r, _ := newTestRun(t, &fakeConn{download: dl.download}, sink)
	// Dan (photo 501) is a private chat and a sender; Familia (610) is a group.
	if err := r.ensureChat(model.Chat{SourceID: "user:1", Kind: model.ChatPrivate, Title: "Dan Whitfield"}); err != nil {
		t.Fatal(err)
	}
	if err := r.upsertSender(model.Sender{SourceID: "user:1", DisplayName: "Dan Whitfield"}); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureChat(model.Chat{SourceID: "chat:10", Kind: model.ChatGroup, Title: "Familia"}); err != nil {
		t.Fatal(err)
	}
	working(t, r, "the pictures", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return len(sink.avatars) == 3 })
	got := dl.list()
	slices.Sort(got)
	if !slices.Equal(got, []string{"avatar:501", "avatar:610"}) {
		t.Errorf("downloads = %v; Dan's picture serves his chat and him", got)
	}
	if string(sink.avatars[model.AvatarRef{Kind: model.AvatarSender, SourceID: "user:1", AvatarID: "photo:501"}]) != "bytes of avatar:501" {
		t.Errorf("avatars = %v", sink.avatars)
	}

	// A new run with the same pictures downloads nothing.
	r2, _ := newTestRun(t, &fakeConn{download: dl.download}, sink)
	r2.ensureChat(model.Chat{SourceID: "chat:10", Kind: model.ChatGroup, Title: "Familia"})
	r2.ensureChat(model.Chat{SourceID: "user:1", Kind: model.ChatPrivate, Title: "Dan Whitfield"})
	working(t, r2, "nothing", func() bool { return true })
	if len(dl.list()) != 2 {
		t.Errorf("pictures already kept were fetched again: %v", dl.list())
	}
}

// Review finding: a full disk while storing a file was reported as a failed
// download, so every queued file was marked failed instead of the archive
// pausing.
func TestAFullDiskStopsTheDownloads(t *testing.T) {
	dl := &downloads{}
	sink := newMemSink()
	sink.failMedia = fmt.Errorf("%w: no space left on device", connector.ErrStorage)
	r, _ := newTestRun(t, &fakeConn{download: dl.download}, sink)
	r.store(photoMessage(1, 101, 1), true)
	done := make(chan error, 1)
	go func() { done <- r.mediaWorker(context.Background()) }()
	select {
	case err := <-done:
		if !errors.Is(err, connector.ErrStorage) {
			t.Errorf("the worker stopped with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the worker carried on after a full disk")
	}
	if len(sink.statuses) != 0 {
		t.Errorf("statuses = %v; nothing should be marked failed", sink.statuses)
	}
}
