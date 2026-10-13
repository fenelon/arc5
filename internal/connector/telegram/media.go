package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

const mediaTries = 3

// download is an attachment to fetch. A nil Location means the message has
// to be fetched again first, as for a file the owner asked for.
type download struct {
	ref  model.AttachmentRef
	item mediaItem
}

// avatarJob is a picture to fetch, and the chats and senders it belongs to:
// a private chat's picture is also its person's.
type avatarJob struct {
	peer tg.PeerClass
	id   int64
	refs []model.AvatarRef
}

// mediaQueue holds downloads in three lanes: live media first, so a file on
// an auto-deleting message is fetched before it goes, then pictures, then
// media found by the backfill.
type mediaQueue struct {
	mu       sync.Mutex
	live     []download
	backfill []download
	avatars  []*avatarJob
	wake     chan struct{}
}

func (q *mediaQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (r *run) queue() *mediaQueue {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.media == nil {
		r.media = &mediaQueue{wake: make(chan struct{}, 1)}
	}
	return r.media
}

// queueMedia hands the attachments the archive wants downloaded to the
// downloader, in the live or the backfill lane.
func (r *run) queueMedia(chat, message string, refs []model.AttachmentRef, media []mediaItem, live bool) {
	q := r.queue()
	q.mu.Lock()
	for _, ref := range refs {
		for _, item := range media {
			if item.Position != ref.Position || item.Location == nil {
				continue
			}
			d := download{ref: ref, item: item}
			if live {
				q.live = append(q.live, d)
			} else {
				q.backfill = append(q.backfill, d)
			}
		}
	}
	q.mu.Unlock()
	q.signal()
}

// next takes the most urgent piece of work.
func (q *mediaQueue) next() (*download, *avatarJob) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case len(q.live) > 0:
		d := q.live[0]
		q.live = q.live[1:]
		return &d, nil
	case len(q.avatars) > 0:
		a := q.avatars[0]
		q.avatars = q.avatars[1:]
		return nil, a
	case len(q.backfill) > 0:
		d := q.backfill[0]
		q.backfill = q.backfill[1:]
		return &d, nil
	}
	return nil, nil
}

// mediaWorker downloads until ctx ends. Only a storage failure or a dead
// session stops it; a file that cannot be had is marked so.
func (r *run) mediaWorker(ctx context.Context) error {
	q := r.queue()
	for {
		for {
			select {
			case ref := <-r.sink.RequestedMedia():
				q.mu.Lock()
				q.live = append(q.live, download{ref: ref})
				q.mu.Unlock()
				continue
			default:
			}
			break
		}
		d, a := q.next()
		var err error
		switch {
		case d != nil:
			err = r.fetch(ctx, *d)
		case a != nil:
			err = r.fetchAvatar(ctx, a)
		default:
			select {
			case <-ctx.Done():
				return nil
			case <-q.wake:
			case ref := <-r.sink.RequestedMedia():
				q.mu.Lock()
				q.live = append(q.live, download{ref: ref})
				q.mu.Unlock()
			}
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && (errors.Is(err, connector.ErrStorage) || unauthorized(err)) {
			return err
		}
	}
}

// fetch downloads one attachment, refreshing its file reference once if
// Telegram says it expired, and trying three times in all.
func (r *run) fetch(ctx context.Context, d download) error {
	backfill := false
	if d.item.Location == nil {
		item, found, err := r.refresh(ctx, d.ref)
		if err != nil {
			return r.giveUp(d.ref, err)
		}
		if !found {
			return r.sink.SetAttachmentStatus(d.ref, model.StatusUnavailable, "Telegram no longer has this message")
		}
		d.item = item
	} else {
		backfill = !d.item.live
	}
	refreshed := false
	var last error
	for try := range mediaTries {
		if try > 0 {
			if err := r.c.sleep(ctx, time.Duration(1<<(try-1))*time.Second); err != nil {
				return err
			}
		}
		if backfill {
			if err := r.pace(ctx); err != nil {
				return err
			}
		}
		err := r.call(ctx, func() error { return r.put(ctx, d) })
		switch {
		case err == nil:
			return nil
		case errors.Is(err, connector.ErrStorage), unauthorized(err), ctx.Err() != nil:
			return err
		case fileGone(err):
			return r.sink.SetAttachmentStatus(d.ref, model.StatusUnavailable, "Telegram no longer has this file")
		case referenceExpired(err) && !refreshed:
			refreshed = true
			item, found, rerr := r.refresh(ctx, d.ref)
			if rerr == nil && !found {
				return r.sink.SetAttachmentStatus(d.ref, model.StatusUnavailable, "Telegram no longer has this message")
			}
			if rerr == nil {
				d.item = item
			}
		}
		last = err
	}
	return r.giveUp(d.ref, last)
}

func (r *run) giveUp(ref model.AttachmentRef, err error) error {
	if errors.Is(err, connector.ErrStorage) || unauthorized(err) {
		return err
	}
	return r.sink.SetAttachmentStatus(ref, model.StatusFailed, err.Error())
}

// put streams a file from Telegram into the archive. A download that fails
// part way fails the archive's read too, so nothing half-written is kept.
func (r *run) put(ctx context.Context, d download) error {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := r.conn.Download(ctx, d.item.Location, pw)
		pw.CloseWithError(err)
		done <- err
	}()
	putErr := r.sink.PutMedia(d.ref, pr)
	pr.CloseWithError(errors.New("the archive stopped reading"))
	if err := <-done; err != nil {
		return err
	}
	return putErr
}

// refresh fetches a message again for a fresh file location.
func (r *run) refresh(ctx context.Context, ref model.AttachmentRef) (mediaItem, bool, error) {
	peer, ok := r.inputPeer(ref.ChatSourceID)
	id, err := strconv.Atoi(ref.MessageSourceID)
	if !ok || err != nil {
		return mediaItem{}, false, nil
	}
	var page historyPage
	if err := r.call(ctx, func() error {
		var err error
		page, err = r.conn.Messages(ctx, peer, []int{id})
		return err
	}); err != nil {
		return mediaItem{}, false, err
	}
	r.learn(page.Users, page.Chats)
	for _, m := range page.Messages {
		if m.GetID() != id {
			continue
		}
		r.mu.Lock()
		_, media, ok := mapMessage(m, r.e, r.opts.keepEphemeral)
		r.mu.Unlock()
		if !ok {
			return mediaItem{}, false, nil
		}
		for _, item := range media {
			if item.Position == ref.Position && item.Location != nil {
				item.live = true
				return item, true, nil
			}
		}
	}
	return mediaItem{}, false, nil
}

// queueAvatar asks for a chat's or sender's picture if it is not kept yet.
func (r *run) queueAvatar(kind model.AvatarKind, sourceID string) error {
	peer, ok := sourcePeer(sourceID)
	if !ok {
		return nil
	}
	r.mu.Lock()
	photo := r.e.photo(peer)
	r.mu.Unlock()
	if photo == 0 {
		return nil
	}
	ref := model.AvatarRef{Kind: kind, SourceID: sourceID, AvatarID: fmt.Sprintf("photo:%d", photo)}
	stored, err := r.sink.AvatarStored(ref)
	if err != nil || stored {
		return err
	}
	q := r.queue()
	q.mu.Lock()
	defer q.signal()
	defer q.mu.Unlock()
	for _, job := range q.avatars {
		if job.id == photo && peerSourceID(job.peer) == sourceID {
			job.refs = append(job.refs, ref)
			return nil
		}
	}
	q.avatars = append(q.avatars, &avatarJob{peer: peer, id: photo, refs: []model.AvatarRef{ref}})
	return nil
}

// fetchAvatar downloads a picture and keeps it for everyone who has it.
// Pictures are small and not essential: one that cannot be had is skipped
// and tried again by a later run.
func (r *run) fetchAvatar(ctx context.Context, job *avatarJob) error {
	in, ok := r.inputPeer(peerSourceID(job.peer))
	if !ok {
		return nil
	}
	var buf bytes.Buffer
	err := r.call(ctx, func() error {
		buf.Reset()
		return r.conn.Download(ctx, &tg.InputPeerPhotoFileLocation{Peer: in, PhotoID: job.id}, &buf)
	})
	if err != nil {
		if unauthorized(err) {
			return err
		}
		return nil
	}
	for _, ref := range job.refs {
		if err := r.sink.PutAvatar(ref, bytes.NewReader(buf.Bytes())); err != nil {
			return err
		}
	}
	return nil
}

// photo is the identity of a user's or chat's current picture, or 0.
func (e *entities) photo(p tg.PeerClass) int64 {
	switch p := p.(type) {
	case *tg.PeerUser:
		if u := e.users[p.UserID]; u != nil {
			if ph, ok := u.Photo.(*tg.UserProfilePhoto); ok {
				return ph.PhotoID
			}
		}
	case *tg.PeerChat:
		if c := e.chats[p.ChatID]; c != nil {
			if ph, ok := c.Photo.(*tg.ChatPhoto); ok {
				return ph.PhotoID
			}
		}
	case *tg.PeerChannel:
		if c := e.channels[p.ChannelID]; c != nil {
			if ph, ok := c.Photo.(*tg.ChatPhoto); ok {
				return ph.PhotoID
			}
		}
	}
	return 0
}
