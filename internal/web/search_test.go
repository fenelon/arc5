package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// marinaSite has one source with the conversations the approved mockup
// searches for "marina".
type marinaSite struct {
	*site
	acc                     int64
	flight, deposit, keys   int64 // message IDs
	dan, family, marinaChat int64 // chat IDs
	marina                  int64 // sender ID
}

func newMarinaSite(t *testing.T) *marinaSite {
	t.Helper()
	s := newSite(t, "")
	s.signIn()
	s.srv.deps.Now = func() time.Time { return noon.Add(7 * 24 * time.Hour) }
	m := &marinaSite{site: s, acc: s.account("Personal", "telegram")}
	for _, sn := range []model.Sender{{SourceID: "marina", DisplayName: "Marina Sokolova"}, {SourceID: "pablo", DisplayName: "Pablo Ruiz"}, {SourceID: "dan", DisplayName: "Dan Whitfield"}} {
		if err := s.store.UpsertSender(m.acc, sn); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.UpsertChat(m.acc, model.Chat{SourceID: "family", Kind: model.ChatGroup, Title: "Familia: Marina y Pablo"}); err != nil {
		t.Fatal(err)
	}
	from := func(sender string) func(*model.Message) {
		return func(msg *model.Message) { msg.SenderSourceID = sender }
	}
	m.flight = s.say(m.acc, "Dan Whitfield", "1", "Marina's flight lands at 18:40. Can you pick her up?", noon.Add(-48*time.Hour), from("dan"))
	if err := s.store.MarkDeleted(m.acc, "Dan Whitfield", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	m.deposit = s.say(m.acc, "Dan Whitfield", "2", "Sent the deposit to Marina yesterday, receipt attached.", noon.Add(-24*time.Hour), func(msg *model.Message) {
		msg.Outgoing = true
		msg.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "deposit-marina.pdf", MimeType: "application/pdf", Size: 10}}
	})
	m.keys = s.say(m.acc, "family", "1", "Dile a Marina que las llaves están en el buzón.", noon.Add(-time.Hour), from("pablo"))
	s.say(m.acc, "Marina Sokolova", "1", "Спасибо, что встретил!", noon, from("marina"))
	s.say(m.acc, "Saved Messages", "1", "Passport renewal: form 11", noon.Add(-72*time.Hour))
	m.dan, m.family, m.marinaChat = s.chatID(m.acc, "Dan Whitfield"), s.chatID(m.acc, "Familia: Marina y Pablo"), s.chatID(m.acc, "Marina Sokolova")
	if err := s.store.Reader().QueryRow(`SELECT id FROM senders WHERE source_id = 'marina'`).Scan(&m.marina); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSearchShowsChatsPeopleAndMessages(t *testing.T) {
	m := newMarinaSite(t)
	body := m.page(fmt.Sprintf("/s/%d?q=marina", m.acc))
	has(t, body,
		`<input class="search__input" type="search" name="q" value="marina"`,
		"Chats and people",
		`<mark class="hit">Marina</mark> Sokolova`, "Private chat, last message 28 Sep",
		`Familia: <mark class="hit">Marina</mark> y Pablo`, "Group, last message 28 Sep",
		fmt.Sprintf(`href="/s/%d/c/%d?q=marina"`, m.acc, m.marinaChat),
		"Everything from this person, in any chat", fmt.Sprintf(`href="/s/%d?q=marina&amp;person=%d"`, m.acc, m.marina),
		"<h2 class=\"results__heading\">Messages</h2>",
		`<mark class="hit">Marina</mark>&#39;s flight lands`, "Deleted at source",
		`You: Sent the deposit to <mark class="hit">Marina</mark>`,
		`File: deposit-<mark class="hit">marina</mark>.pdf`,
		`Pablo Ruiz: Dile a <mark class="hit">Marina</mark>`,
		fmt.Sprintf(`href="/s/%d/c/%d?at=%d&amp;q=marina"`, m.acc, m.dan, m.flight))
	lacks(t, body, "Passport renewal", "Show more messages")
}

func TestSearchFragment(t *testing.T) {
	m := newMarinaSite(t)
	rec := m.get(fmt.Sprintf("/s/%d/results?q=marina", m.acc))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Security-Policy") != csp {
		t.Fatalf("fragment: %d %v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	has(t, body, "Chats and people", `<mark class="hit">Marina</mark> Sokolova`)
	lacks(t, body, "<html", "<header")
	has(t, m.get(fmt.Sprintf("/s/%d/results?q=", m.acc)).Body.String(), "Passport renewal", `class="row`)
	if rec := m.get("/s/9999/results?q=marina"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown source: %d", rec.Code)
	}
}

func TestSearchNothingFound(t *testing.T) {
	m := newMarinaSite(t)
	has(t, m.page(fmt.Sprintf("/s/%d?q=zzzz", m.acc)), "Nothing matches “zzzz”.")
}

func TestSearchKeepsToTheChosenSource(t *testing.T) {
	m := newMarinaSite(t)
	work := m.account("Work", "telegram")
	m.say(work, "Ana", "1", "Marina from work", noon)
	has(t, m.page(fmt.Sprintf("/s/%d?q=marina", work)), "</mark> from work")
	lacks(t, m.page(fmt.Sprintf("/s/%d?q=marina", work)), "flight lands")
	lacks(t, m.page(fmt.Sprintf("/s/%d?q=marina", m.acc)), "from work")
	has(t, m.page("/s/chats?q=marina"), "</mark> from work", "flight lands")
}

func TestOpeningAHitKeepsTheResultsAndMarksTheMessage(t *testing.T) {
	m := newMarinaSite(t)
	body := m.page(fmt.Sprintf("/s/%d/c/%d?at=%d&q=marina", m.acc, m.dan, m.flight))
	has(t, body, "Chats and people", "result result--current",
		`<p class="msg__text"><mark class="hit">Marina</mark>&#39;s flight lands`,
		fmt.Sprintf(`class="back only-phone" href="/s/%d?q=marina"`, m.acc),
		`<input class="search__input" type="search" name="q" value="marina"`)
	if strings.Count(body, "result--current") != 1 {
		t.Error("more than one result is marked as open")
	}
	// Other messages on the page are not marked, nor is the target without a search.
	has(t, body, `<p class="msg__text">Sent the deposit to Marina yesterday`)
	lacks(t, m.page(fmt.Sprintf("/s/%d/c/%d?at=%d", m.acc, m.dan, m.flight)), `<mark class="hit">`)
}

func TestPersonShowsTheirMessages(t *testing.T) {
	m := newMarinaSite(t)
	body := m.page(fmt.Sprintf("/s/%d?q=marina&person=%d", m.acc, m.marina))
	has(t, body, "Everything from Marina Sokolova", "Спасибо, что встретил!",
		fmt.Sprintf(`href="/s/%d?q=marina">All results`, m.acc))
	lacks(t, body, "Chats and people", "flight lands", "Dile a")

	other := m.account("Work", "telegram")
	if rec := m.get(fmt.Sprintf("/s/%d?q=marina&person=%d", other, m.marina)); rec.Code != http.StatusNotFound {
		t.Errorf("a person from another source: %d, want 404", rec.Code)
	}
	if rec := m.get(fmt.Sprintf("/s/%d?q=marina&person=banana", m.acc)); rec.Code != http.StatusNotFound {
		t.Errorf("a made-up person: %d, want 404", rec.Code)
	}
}

func TestSearchPaging(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	for i := range 120 {
		s.say(acc, "Cats", fmt.Sprint(i), fmt.Sprintf("cats number %d", i), noon.Add(time.Duration(i)*time.Minute))
	}
	first := s.page(fmt.Sprintf("/s/%d?q=cats", acc))
	if n := strings.Count(first, `<a class="result`); n != 50 {
		t.Errorf("first page has %d results, want 50", n)
	}
	has(t, first, fmt.Sprintf(`href="/s/%d?q=cats&amp;page=2"`, acc), "Show more messages", "Chats and people")
	third := s.page(fmt.Sprintf("/s/%d?q=cats&page=3", acc))
	if n := strings.Count(third, `<a class="result`); n != 20 {
		t.Errorf("third page has %d results, want 20", n)
	}
	has(t, third, fmt.Sprintf(`href="/s/%d?q=cats">First results`, acc))
	lacks(t, third, "Show more messages", "Chats and people")
	if body := s.page(fmt.Sprintf("/s/%d?q=cats&page=-4", acc)); strings.Count(body, `<a class="result`) != 50 {
		t.Error("a nonsense page number does not give the first page")
	}
}

// Review Focus: hostile text in a query or a result is shown as text.
func TestSearchShowsHostileTextAsText(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, `<img src=x onerror=alert(1)>`, "1", `<script>alert("x")</script> script here`, noon)
	q := url.QueryEscape(`"><script>alert(2)</script>`)
	for path, escaped := range map[string]string{
		fmt.Sprintf("/s/%d?q=%s", acc, q):          `value="&#34;&gt;&lt;script&gt;alert(2)&lt;/script&gt;"`,
		fmt.Sprintf("/s/%d?q=script", acc):         "&lt;img src=x onerror=alert(1)&gt;",
		fmt.Sprintf("/s/%d/results?q=script", acc): "alert(&#34;x&#34;)&lt;/",
	} {
		body := s.page(path)
		if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") {
			t.Errorf("%s: hostile text reached the page as markup", path)
		}
		// The text is there, as text: the test cannot pass by showing nothing.
		has(t, body, escaped)
	}
}
