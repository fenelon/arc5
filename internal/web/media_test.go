package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

// storeFiles stores one message with the given attachments, fills the first
// stored of them with content, and returns their attachment IDs.
func storeFiles(t *testing.T, s *site, stored int, attachments ...model.Attachment) []int64 {
	t.Helper()
	acc := s.account("Personal", "telegram")
	id := s.say(acc, "Files", "1", "", noon, func(m *model.Message) { m.Attachments = attachments })
	for i := range stored {
		ref := model.AttachmentRef{ChatSourceID: "Files", MessageSourceID: "1", Position: i}
		if err := s.store.PutMedia(acc, ref, strings.NewReader(fmt.Sprintf("content %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.store.Attachments(id)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	return ids
}

func TestMediaIsServedToTheOwnerOnly(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 1, model.Attachment{Kind: model.AttachPhoto, FileName: "plan.jpg", MimeType: "image/jpeg", Size: 9})
	path := fmt.Sprintf("/media/%d", ids[0])

	rec := s.get(path)
	if rec.Code != http.StatusOK || rec.Body.String() != "content 0" {
		t.Fatalf("media: %d %q", rec.Code, rec.Body)
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/jpeg",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox; default-src 'none'",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if d := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(d, "inline") || !strings.Contains(d, "plan.jpg") {
		t.Errorf("Content-Disposition = %q", d)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "private") {
		t.Errorf("Cache-Control = %q; archived media must not be cached by shared caches", rec.Header().Get("Cache-Control"))
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Range", "bytes=0-3")
	ranged := s.do(req)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "cont" {
		t.Errorf("range request: %d %q; players need it to seek", ranged.Code, ranged.Body)
	}

	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if got := stranger.get(path); !redirectsTo(got, "/login") {
		t.Fatalf("media without a session: %d", got.Code)
	}
}

// Review Focus 5: files that can carry script are downloads, never pages.
func TestDangerousTypesAreDownloads(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 4,
		model.Attachment{Kind: model.AttachDocument, FileName: "page.html", MimeType: "text/html", Size: 9},
		model.Attachment{Kind: model.AttachPhoto, FileName: "drawing.svg", MimeType: "image/svg+xml", Size: 9},
		model.Attachment{Kind: model.AttachPhoto, FileName: "liar.jpg", MimeType: "IMAGE/JPEG; charset=x", Size: 9},
		model.Attachment{Kind: model.AttachDocument, FileName: "a\"b\r\nSet-Cookie: x=1.txt", MimeType: "", Size: 9},
	)
	for i, wantInline := range []bool{false, false, true, false} {
		rec := s.get(fmt.Sprintf("/media/%d", ids[i]))
		if rec.Code != http.StatusOK {
			t.Fatalf("attachment %d: status %d", i, rec.Code)
		}
		d, ct := rec.Header().Get("Content-Disposition"), rec.Header().Get("Content-Type")
		if inline := strings.HasPrefix(d, "inline"); inline != wantInline {
			t.Errorf("attachment %d: disposition %q, content type %q", i, d, ct)
		}
		if !wantInline && ct != "application/octet-stream" {
			t.Errorf("attachment %d: content type %q, want application/octet-stream", i, ct)
		}
		if rec.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("attachment %d: headers %v", i, rec.Header())
		}
	}
}

func TestMediaThatIsNotStored(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 0, model.Attachment{Kind: model.AttachDocument, FileName: "waiting.bin", Size: 9})
	for _, path := range []string{fmt.Sprintf("/media/%d", ids[0]), "/media/999999", "/media/banana"} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestFetchNow(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 0, model.Attachment{Kind: model.AttachVideo, FileName: "big.mp4", Size: 9})
	path := fmt.Sprintf("/media/%d/fetch", ids[0])

	rec := s.post(path, url.Values{"back": {"/s/chats/c/1?at=5"}})
	if !redirectsTo(rec, "/s/chats/c/1?at=5") || len(s.runner.requested) != 1 || s.runner.requested[0] != ids[0] {
		t.Fatalf("fetch: %d to %q, requested %v", rec.Code, rec.Header().Get("Location"), s.runner.requested)
	}
	// Only an address on this site is followed back.
	for _, back := range []string{"https://evil.example/", "//evil.example/", `/\evil.example`, "", "javascript:alert(1)", "/\t/evil.example/", "/\x01/x", "/\x7f"} {
		if rec := s.post(path, url.Values{"back": {back}}); !redirectsTo(rec, "/") {
			t.Errorf("back %q: redirected to %q, want /", back, rec.Header().Get("Location"))
		}
	}

	// Review Focus 2: no change without the token, none without a session.
	before := len(s.runner.requested)
	if rec := s.post(path, url.Values{"csrf": {"made-up"}}); rec.Code != http.StatusForbidden {
		t.Errorf("fetch with a wrong token: %d", rec.Code)
	}
	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := stranger.post(path, url.Values{"csrf": {s.csrf}}); !redirectsTo(rec, "/login") {
		t.Errorf("fetch without a session: %d", rec.Code)
	}
	if len(s.runner.requested) != before {
		t.Fatal("a refused request still asked for the download")
	}

	s.runner.err = archive.ErrNotFound
	if rec := s.post(path, nil); rec.Code != http.StatusNotFound {
		t.Errorf("fetch of something already stored or missing: %d, want 404", rec.Code)
	}
	s.runner.err = errors.New("disk on fire")
	if rec := s.post(path, nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("fetch that fails: %d, want 500", rec.Code)
	}
	if rec := s.post("/media/banana/fetch", nil); rec.Code != http.StatusNotFound {
		t.Errorf("fetch of a bad id: %d", rec.Code)
	}
}

// View-once media is fetched only by a source set to keep it; the owner
// cannot ask for it afterwards, so nothing reports it as opened.
func TestFetchRefusesWithheldViewOnceMedia(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 0, model.Attachment{Kind: model.AttachPhoto, FileName: "once.jpg", Size: 9, Ephemeral: true, NoFetch: true, NoFetchReason: "self-destructing media is not archived"})
	if rec := s.post(fmt.Sprintf("/media/%d/fetch", ids[0]), nil); rec.Code != http.StatusConflict || len(s.runner.requested) != 0 {
		t.Fatalf("fetching withheld view-once media: %d, requested %v", rec.Code, s.runner.requested)
	}
}

func TestMissingFileIs404(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 1, model.Attachment{Kind: model.AttachPhoto, FileName: "plan.jpg", MimeType: "image/jpeg", Size: 9})
	file, err := s.store.AttachmentFile(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	if rec := s.get(fmt.Sprintf("/media/%d", ids[0])); rec.Code != http.StatusNotFound {
		t.Errorf("a file missing from disk: %d, want 404", rec.Code)
	}
}
