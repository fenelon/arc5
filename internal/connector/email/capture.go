package email

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

const (
	// batch is how many messages one history step fetches.
	batch = 50
	// maxMessage is the largest message downloaded whole; a larger one is
	// stored from its headers.
	maxMessage = 100 << 20
	tooLarge   = "This message is larger than arc5 downloads (100 MB). Its headers are kept; the message itself is still on the server."
)

// checkpoint is where capture stands in one folder, kept in the connector's
// state under "folder:" and the folder's name. UIDs from Next on are new
// mail; history is done for UIDs from Oldest up to Next. A checkpoint is
// saved only after the messages it covers are stored.
type checkpoint struct {
	Validity uint32 // the UIDVALIDITY these UIDs belong to
	Next     uint32
	Oldest   uint32
	Exists   uint32 // how many messages the folder held when last looked at
	Since    int64  // the age limit history was fetched under, Unix seconds; 0 for all
	Done     bool   // history is complete under Since
}

func checkpointKey(folder string) string { return "folder:" + folder }

// run is one run of the connector.
type run struct {
	c     *Connector
	cl    client
	sink  connector.MailSink
	state connector.State
	o     options
}

func (c *Connector) run(ctx context.Context, sink connector.MailSink, state connector.State, a account, o options) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sink.ReportProgress(connector.Progress{Phase: connector.PhaseConnecting})
	cl, err := c.dial(ctx, a)
	if errors.Is(err, errLoginRefused) {
		return connector.ErrNeedsLogin
	}
	if err != nil {
		return err
	}
	defer cl.Close()
	if err := sink.SetAccountAddress(a.Address); err != nil {
		return err
	}
	r := &run{c: c, cl: cl, sink: sink, state: state, o: o}
	go func() {
		if err := r.serveRequests(ctx); err != nil {
			cancel(err)
		}
	}()
	for {
		if err := r.cycle(ctx); err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return err
		}
		if c.afterCycle != nil {
			c.afterCycle()
		}
	}
}

// cycle takes in new mail in every archived folder, then deletions, then a
// slice of history; with history done, it waits for the server's news.
func (r *run) cycle(ctx context.Context) error {
	list, err := r.cl.List(ctx)
	if err != nil {
		return err
	}
	var archived []model.Folder
	for _, f := range captureOrder(folderPlan(list)) {
		if err := r.sink.UpsertFolder(f); err != nil {
			return err
		}
		on, err := r.sink.FolderArchived(f.SourceID)
		if err != nil {
			return err
		}
		if on {
			archived = append(archived, f)
		}
	}
	seen := map[string]looked{}
	var open []model.Folder // the archived folders the server lets us open
	var trouble []string
	for _, f := range archived {
		l, err := r.newMail(ctx, f.SourceID)
		if errors.Is(err, errServerRefused) {
			// The server will not open this folder now: pass it over rather
			// than stop every other folder behind it.
			trouble = append(trouble, fmt.Sprintf("%s could not be opened: %v", f.Name, err))
			continue
		}
		if err != nil {
			return err
		}
		seen[f.SourceID] = l
		open = append(open, f)
	}
	archived = open
	for _, f := range archived {
		if err := r.deletions(ctx, f.SourceID, seen[f.SourceID]); err != nil {
			return err
		}
	}
	done, err := r.history(ctx, archived)
	if err != nil {
		return err
	}
	phase := connector.PhaseBackfill
	if done == len(archived) {
		phase = connector.PhaseLive
	}
	r.sink.ReportProgress(connector.Progress{Phase: phase, ChatsDone: done, ChatsTotal: len(archived), Detail: strings.Join(trouble, "; ")})
	if done < len(archived) && !r.o.paused {
		return nil // more history first
	}
	watched := watchedFolder(archived)
	if watched == "" {
		return r.c.sleep(ctx, r.c.wait)
	}
	if _, err := r.cl.Select(ctx, watched); err != nil {
		return err
	}
	return r.cl.Idle(ctx, r.c.wait)
}

// watchedFolder is the folder to IDLE on: All Mail, where every new message
// shows, else the Inbox, else the first archived folder.
func watchedFolder(archived []model.Folder) string {
	for _, role := range []model.FolderRole{model.FolderAll, model.FolderInbox} {
		if i := slices.IndexFunc(archived, func(f model.Folder) bool { return f.Role == role }); i >= 0 {
			return archived[i].SourceID
		}
	}
	if len(archived) > 0 {
		return archived[0].SourceID
	}
	return ""
}

func (r *run) load(folder string) (checkpoint, error) {
	var cp checkpoint
	_, err := loadJSON(r.state, checkpointKey(folder), &cp)
	return cp, err
}

func (r *run) save(folder string, cp checkpoint) error {
	return saveJSON(r.state, checkpointKey(folder), cp)
}

// looked is what the new-mail pass saw of a folder.
type looked struct {
	messages uint32 // the folder's count when opened
	added    int    // new messages taken in
	fresh    bool   // first seen, or renumbered: nothing to compare yet
}

// newMail takes in the messages that arrived in a folder since it was last
// looked at. A folder seen for the first time, or renumbered by the server,
// starts its history from the top.
func (r *run) newMail(ctx context.Context, folder string) (looked, error) {
	sel, err := r.cl.Select(ctx, folder)
	if err != nil {
		return looked{}, err
	}
	cp, err := r.load(folder)
	if err != nil {
		return looked{}, err
	}
	if cp.Validity != sel.UIDValidity {
		if cp.Validity != 0 {
			// The server renumbered the folder: its UIDs mean nothing now.
			// Messages are matched by Message-ID as history reads it again.
			if err := r.sink.ResetFolder(folder); err != nil {
				return looked{}, err
			}
		}
		cp = checkpoint{Validity: sel.UIDValidity, Next: sel.UIDNext, Oldest: sel.UIDNext, Exists: sel.Messages}
		return looked{messages: sel.Messages, fresh: true}, r.save(folder, cp)
	}
	l := looked{messages: sel.Messages}
	if sel.UIDNext <= cp.Next {
		return l, nil
	}
	uids, err := r.cl.Search(ctx, cp.Next, 0, time.Time{})
	if err != nil {
		return l, err
	}
	for len(uids) > 0 {
		n := min(batch, len(uids))
		if err := r.store(ctx, folder, uids[:n]); err != nil {
			return l, err
		}
		l.added += n
		cp.Next = uids[n-1] + 1
		if err := r.save(folder, cp); err != nil {
			return l, err
		}
		uids = uids[n:]
	}
	cp.Next = max(cp.Next, sel.UIDNext)
	return l, r.save(folder, cp)
}

// deletions finds the messages that left a folder: when it holds fewer than
// it did plus the new mail, its UIDs are compared with the archive's.
func (r *run) deletions(ctx context.Context, folder string, l looked) error {
	cp, err := r.load(folder)
	if err != nil || l.fresh {
		return err
	}
	if l.messages == cp.Exists+uint32(l.added) {
		if l.added == 0 {
			return nil
		}
		cp.Exists = l.messages // the new mail is now part of the count to compare with
		return r.save(folder, cp)
	}
	if _, err := r.cl.Select(ctx, folder); err != nil {
		return err
	}
	there, err := r.cl.Search(ctx, 1, 0, time.Time{})
	if err != nil {
		return err
	}
	filed, err := r.sink.FolderUIDs(folder)
	if err != nil {
		return err
	}
	var gone []uint32
	for _, uid := range filed {
		if _, found := slices.BinarySearch(there, uid); !found && uid < cp.Next {
			gone = append(gone, uid)
		}
	}
	if len(gone) > 0 {
		if err := r.sink.LeaveFolder(folder, gone); err != nil {
			return err
		}
	}
	cp.Exists = uint32(len(there))
	return r.save(folder, cp)
}

// history fetches older mail, newest first, until the slice of time is
// used. It returns how many folders have their history complete.
func (r *run) history(ctx context.Context, folders []model.Folder) (int, error) {
	deadline := r.c.now().Add(r.c.slice)
	since := int64(0)
	if !r.o.since.IsZero() {
		since = r.o.since.Unix()
	}
	done := 0
	for _, f := range folders {
		cp, err := r.load(f.SourceID)
		if err != nil {
			return done, err
		}
		if cp.Done && (since < cp.Since || (since == 0 && cp.Since != 0)) {
			// The owner lengthened the limit: read the folder again; mail
			// already stored is only matched, not downloaded.
			cp.Done, cp.Oldest = false, cp.Next
		}
		for !cp.Done && !r.o.paused {
			if r.c.now().After(deadline) && r.c.slice > 0 {
				break
			}
			var uids []uint32
			if cp.Oldest > 1 {
				if _, err := r.cl.Select(ctx, f.SourceID); err != nil {
					return done, err
				}
				if uids, err = r.cl.Search(ctx, 1, cp.Oldest-1, r.o.since); err != nil {
					return done, err
				}
			}
			if len(uids) == 0 {
				cp.Done, cp.Since = true, since
				if err := r.save(f.SourceID, cp); err != nil {
					return done, err
				}
				break
			}
			step := uids[max(0, len(uids)-batch):]
			if err := r.store(ctx, f.SourceID, step); err != nil {
				return done, err
			}
			cp.Oldest = step[0]
			if err := r.save(f.SourceID, cp); err != nil {
				return done, err
			}
			if err := r.c.sleep(ctx, r.o.pause); err != nil {
				return done, err
			}
			if r.c.slice == 0 {
				break // one batch per cycle
			}
		}
		if cp.Done {
			done++
		}
	}
	return done, nil
}

// maxAttempts is how often a message the server will not hand over is tried
// before a note stands in for it, so one message cannot hold up a folder.
const maxAttempts = 3

func attemptsKey(folder string) string { return "attempts:" + folder }

// attempt counts one more try at a message, kept in the state so a crash or
// a dropped connection while fetching it counts too. It reports the tries
// before this one.
func (r *run) attempt(folder string, uid uint32) (int, error) {
	tries := map[uint32]int{}
	if _, err := loadJSON(r.state, attemptsKey(folder), &tries); err != nil {
		return 0, err
	}
	before := tries[uid]
	tries[uid] = before + 1
	return before, saveJSON(r.state, attemptsKey(folder), tries)
}

// fetched forgets the tries at a message that was stored.
func (r *run) fetched(folder string, uid uint32) error {
	tries := map[uint32]int{}
	if _, err := loadJSON(r.state, attemptsKey(folder), &tries); err != nil || tries[uid] == 0 {
		return err
	}
	delete(tries, uid)
	return saveJSON(r.state, attemptsKey(folder), tries)
}

// stand stores a note in place of a message the server would not hand
// over, so the folder's catch-up can go on. The message stays on the server.
func (r *run) stand(ctx context.Context, folder string, h header, reason error) error {
	sel, err := r.cl.Select(ctx, folder)
	if err != nil {
		return err
	}
	id := h.MessageID
	if id == "" {
		id = fmt.Sprintf("uid:%s:%d:%d", folder, sel.UIDValidity, h.UID)
	}
	received := h.Received
	if received.IsZero() {
		received = r.c.now()
	}
	chat := "thread:" + id
	if err := r.sink.UpsertChat(model.Chat{SourceID: chat, Kind: model.ChatThread, Title: "Message arc5 could not download"}); err != nil {
		return err
	}
	text := fmt.Sprintf("arc5 could not download this message from the server after %d tries (%v). It is still in the mailbox, in %s.", maxAttempts, reason, folder)
	if _, err := r.sink.UpsertMessage(model.Message{ChatSourceID: chat, SourceID: id, SentAt: received, Text: text}); err != nil {
		return err
	}
	if err := r.sink.FileInFolder(folder, h.UID, chat, id); err != nil {
		return err
	}
	return r.fetched(folder, h.UID)
}

// store archives messages of the selected folder and files them there. A
// message already archived (by its Message-ID) is only filed.
func (r *run) store(ctx context.Context, folder string, uids []uint32) error {
	heads, err := r.cl.Headers(ctx, uids)
	if errors.Is(err, errServerRefused) {
		if len(uids) > 1 {
			// One message spoils the batch on some servers: go one by one.
			for _, uid := range uids {
				if err := r.store(ctx, folder, []uint32{uid}); err != nil {
					return err
				}
			}
			return nil
		}
		heads = []header{{UID: uids[0]}}
	} else if err != nil {
		return err
	}
	for _, h := range heads {
		if h.MessageID != "" {
			chats, err := r.sink.LookupChats(h.MessageID, threadKinds)
			if err != nil {
				return err
			}
			if len(chats) > 0 {
				if err := r.sink.FileInFolder(folder, h.UID, chats[0], h.MessageID); err != nil {
					return err
				}
				continue
			}
		}
		tries, err := r.attempt(folder, h.UID)
		if err != nil {
			return err
		}
		if tries >= maxAttempts {
			// Earlier tries ended the connection (a timeout on a huge
			// message, say): stop trying.
			if err := r.stand(ctx, folder, h, errors.New("each try failed")); err != nil {
				return err
			}
			continue
		}
		raw, err := r.cl.Fetch(ctx, h.UID, h.Size > maxMessage)
		if errors.Is(err, errGone) {
			continue // expunged since it was listed
		}
		if err != nil {
			if errors.Is(err, errServerRefused) && tries+1 >= maxAttempts {
				if err := r.stand(ctx, folder, h, err); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if h.Size > maxMessage {
			raw = append(raw, []byte(tooLarge+"\r\n")...)
		}
		received := h.Received
		if received.IsZero() {
			received = r.c.now()
		}
		chat, id, err := Store(r.sink, raw, received, func(address string) bool { return r.o.own[address] })
		if err != nil {
			return fmt.Errorf("storing %s UID %d: %w", folder, h.UID, err)
		}
		if err := r.sink.FileInFolder(folder, h.UID, chat, id); err != nil {
			return err
		}
		if err := r.fetched(folder, h.UID); err != nil {
			return err
		}
	}
	return nil
}

// serveRequests answers "Fetch now" from the kept originals. Only a storage
// failure ends it, and with it the run.
func (r *run) serveRequests(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ref, ok := <-r.sink.RequestedMedia():
			if !ok {
				return nil
			}
			err := FetchFromOriginal(r.sink, ref)
			switch {
			case err == nil:
			case errors.Is(err, connector.ErrStorage):
				return err
			default:
				if err := r.sink.SetAttachmentStatus(ref, model.StatusFailed, err.Error()); err != nil && errors.Is(err, connector.ErrStorage) {
					return err
				}
			}
		}
	}
}
