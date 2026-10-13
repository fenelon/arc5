package web

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

func pngPicture(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// picture gives a chat or person of the account a picture and returns its hash.
func (s *site) picture(acc int64, kind model.AvatarKind, sourceID string, content []byte) string {
	s.t.Helper()
	if err := s.store.PutAvatar(acc, model.AvatarRef{Kind: kind, SourceID: sourceID, AvatarID: "p:" + sourceID}, bytes.NewReader(content)); err != nil {
		s.t.Fatal(err)
	}
	if kind == model.AvatarChat {
		info, err := s.store.Chat(s.chatID(acc, sourceID))
		if err != nil {
			s.t.Fatal(err)
		}
		return info.AvatarSHA
	}
	{
		list, _ := s.store.ChatList([]int64{acc}, 1000, 0)
		for _, c := range list {
			w, _ := s.store.ChatMessages(c.ID, archive.Anchor{}, 1000)
			for _, m := range w.Messages {
				if m.SenderAvatarSHA != "" {
					return m.SenderAvatarSHA
				}
			}
		}
		s.t.Fatalf("no picture found for %s", sourceID)
	}
	return ""
}

func TestAvatarsAreServed(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan", "1", "hello", noon)
	sha := s.picture(acc, model.AvatarChat, "Dan", pngPicture(t))

	rec := s.get("/avatars/" + sha)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pngPicture(t)) {
		t.Fatalf("GET the picture: %d", rec.Code)
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/png",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": mediaPolicy,
		"Cache-Control":           "private, max-age=31536000, immutable",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// Only current pictures are served, and only as images.
	s.say(acc, "Eve", "1", "x", noon)
	notImage := s.picture(acc, model.AvatarChat, "Eve", []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`))
	for _, path := range []string{"/avatars/" + notImage, "/avatars/" + strings.Repeat("a", 64), "/avatars/xyz", "/avatars/" + strings.ToUpper(sha)} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, rec.Code)
		}
	}
	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := stranger.get("/avatars/" + sha); !redirectsTo(rec, "/login") {
		t.Errorf("a picture without a session: %d", rec.Code)
	}
}

func TestChatListAndSearchShowPictures(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan Whitfield", "1", "hello", noon)
	s.say(acc, "Marina Sokolova", "1", "hola", noon)
	if err := s.store.UpsertSender(acc, model.Sender{SourceID: "them", DisplayName: "Dan Whitfield"}); err != nil {
		t.Fatal(err)
	}
	chat := s.picture(acc, model.AvatarChat, "Dan Whitfield", pngPicture(t))
	person := s.picture(acc, model.AvatarSender, "them", append(pngPicture(t), 0))

	img := func(sha string) string { return fmt.Sprintf(`<img class="avatar__img" src="/avatars/%s" alt="">`, sha) }
	list := s.page(fmt.Sprintf("/s/%d", acc))
	has(t, list, img(chat), `aria-hidden="true">MS</span>`)
	results := s.page(fmt.Sprintf("/s/%d?q=dan", acc))
	has(t, results, img(chat), img(person))
}

func TestReplacedMediaShowsWithEarlierVersions(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	photo := func(id string) func(*model.Message) {
		return func(m *model.Message) {
			m.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: id + ".jpg", MimeType: "image/jpeg", Size: 10, SourceID: id}}
		}
	}
	s.say(acc, "Dan", "1", "the plan", noon, photo("plan-1"))
	s.say(acc, "Dan", "1", "the plan", noon, photo("plan-2"))
	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Dan")))
	has(t, body, "plan-2.jpg", `<details class="versions">`, "Replaced by an edit", "plan-1.jpg",
		"replaced by an edit before it was downloaded")
	lacks(t, body, "Fetch now", "Try again") // nothing can fetch a replaced file by its old position
}
