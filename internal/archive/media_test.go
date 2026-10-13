package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

// withAttachment stores message id with one document attachment and returns its ref.
func withAttachment(t *testing.T, s *Store, acc int64, id string, size int64) model.AttachmentRef {
	t.Helper()
	m := textMsg(id, "file")
	m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "a.bin", Size: size}}
	mustUpsert(t, s, acc, m)
	return model.AttachmentRef{ChatSourceID: "c1", MessageSourceID: id, Position: 0}
}

func attachment(t *testing.T, s *Store, acc int64, id string) StoredAttachment {
	t.Helper()
	atts, err := s.Attachments(mustMessage(t, s, acc, id).ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 {
		t.Fatalf("message %s has %d attachments, want 1", id, len(atts))
	}
	return atts[0]
}

func mediaFiles(t *testing.T, s *Store) []string {
	t.Helper()
	var files []string
	root := filepath.Join(s.dataDir, "media")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestPutMediaStoresByHashAndKeepsOneCopy(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	content := []byte("the same photo forwarded twice")
	sum := sha256.Sum256(content)
	wantSHA := hex.EncodeToString(sum[:])

	for _, id := range []string{"1", "2"} {
		ref := withAttachment(t, s, acc, id, int64(len(content)))
		if err := s.PutMedia(acc, ref, bytes.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		got := attachment(t, s, acc, id)
		if got.Status != model.StatusStored || got.SHA256 != wantSHA || got.Size != int64(len(content)) {
			t.Fatalf("attachment %s = %+v", id, got)
		}
	}

	files := mediaFiles(t, s)
	if want := []string{s.MediaPath(wantSHA)}; !slices.Equal(files, want) {
		t.Fatalf("media files = %v, want %v", files, want)
	}
	onDisk, err := os.ReadFile(s.MediaPath(wantSHA))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, content) {
		t.Fatal("stored file differs from the content")
	}
	if info, _ := os.Stat(s.MediaPath(wantSHA)); info.Mode().Perm() != 0o600 {
		t.Fatalf("media file mode = %o, want 600", info.Mode().Perm())
	}
	if !strings.HasSuffix(s.MediaPath(wantSHA), filepath.Join("media", wantSHA[:2], wantSHA[2:4], wantSHA)) {
		t.Fatalf("media path = %s", s.MediaPath(wantSHA))
	}
}

type failingReader struct {
	data []byte
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, f.err
	}
	n := copy(p, f.data)
	f.data = f.data[n:]
	return n, nil
}

// Review Focus 4: a download that dies mid-stream leaves nothing behind.
func TestPutMediaReaderFailureLeavesNothing(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ref := withAttachment(t, s, acc, "1", 1000)
	boom := errors.New("connection reset")

	err := s.PutMedia(acc, ref, &failingReader{data: []byte("partial"), err: boom})
	if !errors.Is(err, ErrMediaRead) {
		t.Fatalf("error = %v, want ErrMediaRead", err)
	}
	if got := attachment(t, s, acc, "1"); got.Status != model.StatusPending || got.SHA256 != "" {
		t.Fatalf("attachment after failed download = %+v", got)
	}
	if files := mediaFiles(t, s); len(files) != 0 {
		t.Fatalf("files left behind: %v", files)
	}
}

func TestPutMediaAcceptsEmptyFile(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ref := withAttachment(t, s, acc, "1", 0)
	if err := s.PutMedia(acc, ref, bytes.NewReader(nil)); err != nil {
		t.Fatal(err)
	}
	got := attachment(t, s, acc, "1")
	if got.Status != model.StatusStored || got.Size != 0 {
		t.Fatalf("attachment = %+v", got)
	}
	if _, err := os.Stat(s.MediaPath(got.SHA256)); err != nil {
		t.Fatal(err)
	}
}

func TestPutMediaIsRepeatable(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ref := withAttachment(t, s, acc, "1", 5)
	for range 2 {
		if err := s.PutMedia(acc, ref, strings.NewReader("hello")); err != nil {
			t.Fatal(err)
		}
	}
	if files := mediaFiles(t, s); len(files) != 1 {
		t.Fatalf("media files = %v, want one", files)
	}
}

func TestPutMediaUnknownAttachment(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ref := model.AttachmentRef{ChatSourceID: "c1", MessageSourceID: "nope", Position: 0}
	if err := s.PutMedia(acc, ref, io.LimitReader(nil, 0)); !errors.Is(err, ErrUnknownAttachment) {
		t.Fatalf("error = %v, want ErrUnknownAttachment", err)
	}
	if files := mediaFiles(t, s); len(files) != 0 {
		t.Fatalf("files left behind: %v", files)
	}
}

func TestSetAttachmentStatus(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ref := withAttachment(t, s, acc, "1", 5)

	if err := s.SetAttachmentStatus(acc, ref, model.StatusFailed, "timeout"); err != nil {
		t.Fatal(err)
	}
	if got := attachment(t, s, acc, "1"); got.Status != model.StatusFailed || got.Reason != "timeout" {
		t.Fatalf("attachment = %+v", got)
	}
	for _, bad := range []model.AttachmentStatus{model.StatusStored, model.StatusPending, "weird"} {
		if err := s.SetAttachmentStatus(acc, ref, bad, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("status %q error = %v, want ErrInvalid", bad, err)
		}
	}
	missing := model.AttachmentRef{ChatSourceID: "c1", MessageSourceID: "1", Position: 7}
	if err := s.SetAttachmentStatus(acc, missing, model.StatusFailed, ""); !errors.Is(err, ErrUnknownAttachment) {
		t.Errorf("missing attachment error = %v, want ErrUnknownAttachment", err)
	}
}

func TestRequestMediaRoundTrip(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	if err := s.SetMediaCap(10); err != nil {
		t.Fatal(err)
	}
	ref := withAttachment(t, s, acc, "1", 100) // over the cap, so skipped
	att := attachment(t, s, acc, "1")
	if att.Status != model.StatusSkipped {
		t.Fatalf("precondition: status = %s, want skipped", att.Status)
	}

	gotAcc, gotRef, err := s.RequestMedia(att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotAcc != acc || gotRef != ref {
		t.Fatalf("RequestMedia = %d, %+v", gotAcc, gotRef)
	}
	if got := attachment(t, s, acc, "1"); got.Status != model.StatusPending || !got.Requested || got.Reason != "" {
		t.Fatalf("attachment after request = %+v", got)
	}
	queued, err := s.RequestedMedia(acc)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(queued, []model.AttachmentRef{ref}) {
		t.Fatalf("requested = %+v", queued)
	}

	if err := s.PutMedia(acc, ref, strings.NewReader(strings.Repeat("x", 100))); err != nil {
		t.Fatal(err)
	}
	if got := attachment(t, s, acc, "1"); got.Status != model.StatusStored || got.Requested {
		t.Fatalf("attachment after download = %+v", got)
	}
	if queued, _ := s.RequestedMedia(acc); len(queued) != 0 {
		t.Fatalf("requested after download = %+v", queued)
	}
	if _, _, err := s.RequestMedia(att.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("requesting stored media error = %v, want ErrNotFound", err)
	}
	if _, _, err := s.RequestMedia(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("requesting missing attachment error = %v, want ErrNotFound", err)
	}
}

// A late failure report for a duplicate download must not hide media that was saved.
func TestStoredAttachmentCannotBeDowngraded(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ref := withAttachment(t, s, acc, "1", 5)
	if err := s.PutMedia(acc, ref, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAttachmentStatus(acc, ref, model.StatusUnavailable, "gone at source"); err != nil {
		t.Fatalf("reporting a stored attachment as unavailable: %v", err)
	}
	got := attachment(t, s, acc, "1")
	if got.Status != model.StatusStored || got.SHA256 == "" || got.Reason != "" {
		t.Fatalf("attachment = %+v; want it still stored", got)
	}
}

func TestPendingMediaListsWhatARestartMustRequeue(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	waiting := withAttachment(t, s, acc, "1", 10)
	stored := withAttachment(t, s, acc, "2", 10)
	if err := s.PutMedia(acc, stored, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	asked := withAttachment(t, s, acc, "3", 10)
	if _, _, err := s.RequestMedia(attachment(t, s, acc, "3").ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.PendingMedia(acc)
	if err != nil || len(got) != 1 || got[0].MessageSourceID != waiting.MessageSourceID {
		t.Errorf("PendingMedia = %+v, %v; want only the file nobody asked for", got, err)
	}
	_ = asked
	if other, _ := s.PendingMedia(acc + 1); len(other) != 0 {
		t.Errorf("another account's pending media = %v", other)
	}
}
