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
	sel, err := r.cl.Select(ctx, watched)
	if err != nil {
		return err
	}
	if sel.Messages != seen[watched].messages {
		return nil // mail came or went since the pass: look again now
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
	if sel.UIDNext == 0 {
		// UIDNEXT is optional (RFC 3501): one past the highest UID will do.
		uids, err := r.cl.Search(ctx, 1, 0, time.Time{})
		if err != nil {
			return looked{}, err
		}
		sel.UIDNext = 1
		if len(uids) > 0 {
			sel.UIDNext = uids[len(uids)-1] + 1
		}
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
	cps := make([]checkpoint, len(folders))
	windows := make([]uint32, len(folders))
	for i, f := range folders {
		cp, err := r.load(f.SourceID)
		if err != nil {
			return 0, err
		}
		if cp.Done && (since < cp.Since || (since == 0 && cp.Since != 0)) {
			// The owner lengthened the limit: read the folder again; mail
			// already stored is only matched, not downloaded.
			cp.Done, cp.Oldest = false, cp.Next
		}
		cps[i], windows[i] = cp, historyWindow
	}
	// The folders take turns, a batch each, so none waits for another to
	// finish.
	for !r.o.paused {
		stepped := false
		for i, f := range folders {
			if cps[i].Done {
				continue
			}
			if r.c.slice > 0 && r.c.now().After(deadline) {
				return countDone(cps), nil
			}
			if err := r.historyStep(ctx, f.SourceID, &cps[i], &windows[i], since); err != nil {
				return countDone(cps), err
			}
			stepped = true
		}
		if !stepped || r.c.slice == 0 {
			break // all done, or one batch each per cycle
		}
	}
	return countDone(cps), nil
}

func countDone(cps []checkpoint) int {
	n := 0
	for _, cp := range cps {
		if cp.Done {
			n++
		}
	}
	return n
}

// historyStep stores the next batch of a folder's history, newest first, or
// marks its history done.
func (r *run) historyStep(ctx context.Context, folder string, cp *checkpoint, window *uint32, since int64) error {
	for {
		// Search a window of UIDs just below where history stands,
		// widening it while it finds nothing, rather than listing every
		// remaining UID each time (Gmail counts that against a quota).
		var uids []uint32
		from := uint32(1)
		if cp.Oldest > 1 {
			if cp.Oldest > *window {
				from = cp.Oldest - *window
			}
			if _, err := r.cl.Select(ctx, folder); err != nil {
				return err
			}
			var err error
			if uids, err = r.cl.Search(ctx, from, cp.Oldest-1, r.o.since); err != nil {
				return err
			}
		}
		if len(uids) == 0 && from > 1 {
			cp.Oldest, *window = from, min(*window*4, 1<<30)
			if err := r.save(folder, *cp); err != nil {
				return err
			}
			continue
		}
		if len(uids) == 0 {
			cp.Done, cp.Since = true, since
			return r.save(folder, *cp)
		}
		step := uids[max(0, len(uids)-batch):]
		if err := r.store(ctx, folder, step); err != nil {
			return err
		}
		cp.Oldest = step[0]
		if err := r.save(folder, *cp); err != nil {
			return err
		}
		return r.c.sleep(ctx, r.o.pause)
	}
}

// historyWindow is how many UIDs below where history stands one search
// covers at first.
const historyWindow = 1000

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
	var need []header
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
		need = append(need, h)
	}
	for _, group := range fetchGroups(need) {
		if len(group) > 1 {
			fetched, err := r.many(ctx, folder, group)
			if err != nil {
				return err
			}
			if fetched {
				continue
			}
		}
		for _, h := range group {
			if err := r.one(ctx, folder, h); err != nil {
				return err
			}
		}
	}
	return nil
}

// Messages are fetched together up to these sizes; a larger one alone.
const (
	groupBytes = 16 << 20
	aloneBytes = 4 << 20
)

// fetchGroups splits messages into requests: small ones together, up to
// batch messages and groupBytes, and each large one alone.
func fetchGroups(heads []header) [][]header {
	var (
		out  [][]header
		cur  []header
		size int64
	)
	for _, h := range heads {
		if h.Size > aloneBytes {
			out = append(out, []header{h})
			continue
		}
		if len(cur) > 0 && (len(cur) >= batch || size+h.Size > groupBytes) {
			out, cur, size = append(out, cur), nil, 0
		}
		cur, size = append(cur, h), size+h.Size
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// many fetches a group of messages in one request and stores them. It
// reports false, having stored nothing, when the group should be fetched
// one by one instead: the server refused the request, or an earlier try at
// it ended the connection, so one message may be at fault.
func (r *run) many(ctx context.Context, folder string, group []header) (bool, error) {
	tries := map[uint32]int{}
	if _, err := loadJSON(r.state, attemptsKey(folder), &tries); err != nil {
		return false, err
	}
	uids := make([]uint32, len(group))
	for i, h := range group {
		if tries[h.UID] > 0 {
			return false, nil
		}
		uids[i] = h.UID
	}
	for _, uid := range uids {
		tries[uid] = 1
	}
	if err := saveJSON(r.state, attemptsKey(folder), tries); err != nil {
		return false, err
	}
	raws, err := r.cl.FetchMany(ctx, uids)
	if errors.Is(err, errServerRefused) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, h := range group {
		raw, ok := raws[h.UID]
		if !ok {
			continue // expunged since it was listed
		}
		if err := r.keep(folder, h, raw); err != nil {
			return false, err
		}
	}
	for _, uid := range uids {
		delete(tries, uid)
	}
	return true, saveJSON(r.state, attemptsKey(folder), tries)
}

// one fetches a single message and stores it, counting tries so a message
// the server will not hand over is replaced by a note in the end.
func (r *run) one(ctx context.Context, folder string, h header) error {
	tries, err := r.attempt(folder, h.UID)
	if err != nil {
		return err
	}
	if tries >= maxAttempts {
		// Earlier tries ended the connection (a timeout on a huge
		// message, say): stop trying.
		return r.stand(ctx, folder, h, errors.New("each try failed"))
	}
	raw, err := r.cl.Fetch(ctx, h.UID, h.Size > maxMessage)
	if errors.Is(err, errGone) {
		return nil // expunged since it was listed
	}
	if err != nil {
		if errors.Is(err, errServerRefused) && tries+1 >= maxAttempts {
			return r.stand(ctx, folder, h, err)
		}
		return err
	}
	if h.Size > maxMessage {
		raw = append(raw, []byte(tooLarge+"\r\n")...)
	}
	if err := r.keep(folder, h, raw); err != nil {
		return err
	}
	return r.fetched(folder, h.UID)
}

// keep stores a fetched message and files it in the folder.
func (r *run) keep(folder string, h header, raw []byte) error {
	received := h.Received
	if received.IsZero() {
		received = r.c.now()
	}
	chat, id, err := Store(r.sink, raw, received, func(address string) bool { return r.o.own[address] })
	if err != nil {
		return fmt.Errorf("storing %s UID %d: %w", folder, h.UID, err)
	}
	return r.sink.FileInFolder(folder, h.UID, chat, id)
}

// serveRequests answers "Fetch now" from the kept originals. Only a storage
// failure ends it, and with it the run.
func (r *run) serveRequests(ctx context.Context) error {
	// Files left waiting by an earlier run, such as one that stopped between
	// storing a message and its files, are taken from the originals first.
	waiting, err := r.sink.PendingMedia()
	if err != nil {
		return err
	}
	for _, ref := range waiting {
		if err := r.fetchNow(ref); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ref, ok := <-r.sink.RequestedMedia():
			if !ok {
				return nil
			}
			if err := r.fetchNow(ref); err != nil {
				return err
			}
		}
	}
}

// fetchNow takes one file out of its original. Only a storage failure is
// returned; anything else marks the file failed.
func (r *run) fetchNow(ref model.AttachmentRef) error {
	err := FetchFromOriginal(r.sink, ref)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, connector.ErrStorage):
		return err
	}
	if err := r.sink.SetAttachmentStatus(ref, model.StatusFailed, err.Error()); errors.Is(err, connector.ErrStorage) {
		return err
	}
	return nil
}
