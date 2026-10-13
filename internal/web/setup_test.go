package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/connector"
)

func TestAddASource(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	s.reg.Register(connector.Info{Name: "fake", Title: "Fake", Display: connector.DisplayChats}, func() connector.Connector { return fakeConnector{} })
	s.reg.Register(connector.Info{Name: "hidden", Title: "Hidden", Display: connector.DisplayChats, Hidden: true}, func() connector.Connector { return fakeConnector{} })

	has(t, s.page("/sources"), `href="/sources/new"`, "Add a source")
	body := s.page("/sources/new")
	has(t, body, "<h1>Add a source</h1>", `name="connector" value="fake"`, "Fake")
	lacks(t, body, "Hidden")

	for name, f := range map[string]url.Values{
		"a hidden kind":  {"connector": {"hidden"}, "name": {"Demo"}},
		"an unknown one": {"connector": {"nope"}, "name": {"X"}},
		"no name":        {"connector": {"fake"}, "name": {"  "}},
	} {
		rec := s.post("/sources/new", f)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if accounts, _ := s.store.Accounts(); len(accounts) != 0 {
		t.Fatalf("refused forms created %d accounts", len(accounts))
	}

	rec := s.post("/sources/new", url.Values{"connector": {"fake"}, "name": {"Phone"}})
	accounts, _ := s.store.Accounts()
	if len(accounts) != 1 {
		t.Fatalf("%d accounts after adding one", len(accounts))
	}
	acc := accounts[0]
	login := fmt.Sprintf("/sources/%d/login", acc.ID)
	if !redirectsTo(rec, login) || acc.Enabled || acc.Label != "Phone" || acc.Connector != "fake" {
		t.Fatalf("add: %d to %q, account %+v", rec.Code, rec.Header().Get("Location"), acc)
	}

	has(t, s.page(login), "<title>Log in to Phone - arc5</title>", "Your phone number", "With the country code.", `name="phone" type="text"`)
	if rec := s.post(login, url.Values{"phone": {""}}); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "Type your phone number.") || !strings.Contains(rec.Body.String(), `name="phone"`) {
		t.Fatalf("an empty phone: %d", rec.Code)
	}
	if rec := s.post(login, url.Values{"phone": {"020 7946 0000"}}); !redirectsTo(rec, login) {
		t.Fatalf("phone: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	has(t, s.page(login), "The code", "Sent to 020 7946 0000.", `name="password" type="password"`)
	wrong := s.post(login, url.Values{"code": {"000"}, "password": {"hunter22"}})
	if wrong.Code != http.StatusUnprocessableEntity || !strings.Contains(wrong.Body.String(), "That code is not right.") {
		t.Fatalf("a wrong code: %d", wrong.Code)
	}
	if strings.Contains(wrong.Body.String(), "hunter22") {
		t.Error("the page echoes a secret field")
	}
	closed := fakeFlowsClosed.Load()
	if rec := s.post(login, url.Values{"code": {"12345"}}); !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=login", acc.ID)) {
		t.Fatalf("right code: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if got, _ := s.store.Account(acc.ID); !got.Enabled {
		t.Error("the source is still switched off after logging in")
	}
	if v, _ := s.store.StateGet(acc.ID, "session"); string(v) != "signed in as 020 7946 0000" {
		t.Errorf("session = %q", v)
	}
	if !slices.Contains(s.runner.restarted, acc.ID) {
		t.Error("the connector was not started")
	}
	if fakeFlowsClosed.Load() != closed+1 {
		t.Error("the finished flow was not closed")
	}
	has(t, s.page(fmt.Sprintf("/sources/%d?saved=login", acc.ID)), "Logged in.")
}

func TestLogInAgain(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	s.say(acc, "Dan", "1", "hello", noon)
	s.srv.deps.Now = func() time.Time { return noon.Add(time.Hour) }
	s.runner.status = []connector.Status{{AccountID: acc, State: connector.StateNeedsLogin, Since: noon, LastError: "account needs login"}}
	login := fmt.Sprintf("/sources/%d/login", acc)
	for _, path := range []string{fmt.Sprintf("/s/%d", acc), fmt.Sprintf("/sources/%d", acc)} {
		has(t, s.page(path), fmt.Sprintf(`href="%s">Log in again`, login), "Needs login since 12:00")
	}
	lacks(t, s.page(fmt.Sprintf("/sources/%d", acc)), "account needs login", "Sign this source out")
	s.post(login, nil) // the first step
	s.post(login, url.Values{"phone": {"+1 555"}})
	if rec := s.post(login, url.Values{"code": {"12345"}}); !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=login", acc)) {
		t.Fatalf("log in again: %d", rec.Code)
	}
}

func TestSetupCanBeCancelledAndExpires(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	login := fmt.Sprintf("/sources/%d/login", acc)
	s.page(login)
	s.post(login, url.Values{"phone": {"+1 555"}})
	has(t, s.page(login), "The code")

	closed := fakeFlowsClosed.Load()
	if rec := s.post(login+"/cancel", nil); !redirectsTo(rec, fmt.Sprintf("/sources/%d", acc)) {
		t.Fatalf("cancel: %d", rec.Code)
	}
	if fakeFlowsClosed.Load() != closed+1 {
		t.Error("the cancelled flow was not closed")
	}
	has(t, s.page(login), "Your phone number")

	s.post(login, url.Values{"phone": {"+1 555"}})
	start := time.Now()
	s.srv.deps.Now = func() time.Time { return start.Add(31 * time.Minute) }
	has(t, s.page(login), "Your phone number")

	if rec := s.get("/sources/999/login"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source: %d", rec.Code)
	}
	plain := s.account("Old", "telegram") // a connector with no way to log in
	if rec := s.get(fmt.Sprintf("/sources/%d/login", plain)); rec.Code != http.StatusNotFound {
		t.Errorf("a source that cannot log in: %d", rec.Code)
	}
}
