package web

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/connector"
)

func TestAddASource(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// heldConnector is fakeConnector with a login that waits inside Next when
// given the field named on, as a source answering over the network would,
// until released.
type heldConnector struct {
	fakeConnector
	on               string
	entered, release chan struct{}
}

func (c heldConnector) Setup(state connector.State) connector.SetupFlow {
	return &heldFlow{fakeFlow: fakeFlow{state: state}, held: c}
}

type heldFlow struct {
	fakeFlow
	held heldConnector
}

func (f *heldFlow) Next(ctx context.Context, input map[string]string) (connector.Step, error) {
	if input[f.held.on] != "" {
		close(f.held.entered)
		<-f.held.release
	}
	return f.fakeFlow.Next(ctx, input)
}

// heldAccount adds a source run by a heldConnector.
func (s *site) heldAccount(on string) (int64, heldConnector) {
	s.t.Helper()
	held := heldConnector{on: on, entered: make(chan struct{}), release: make(chan struct{})}
	s.reg.Register(connector.Info{Name: "held", Title: "Held", Display: connector.DisplayChats}, func() connector.Connector { return held })
	acc, err := s.store.CreateAccount("held", "Work", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return acc, held
}

// tab is another browser tab of the same signed-in owner, for requests sent
// at the same time as the site's own.
func (s *site) tab() *site {
	return &site{t: s.t, store: s.store, runner: s.runner, reg: s.reg, srv: s.srv, cookies: maps.Clone(s.cookies), csrf: s.csrf}
}

// Review Focus 4: a login that finishes while the source is being removed
// must not leave its session behind.
func TestRemovingWaitsForALoginInFlight(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc, held := s.heldAccount("code")
	login := fmt.Sprintf("/sources/%d/login", acc)
	s.page(login)
	if rec := s.post(login, url.Values{"phone": {"020 7946 0000"}}); !redirectsTo(rec, login) {
		t.Fatalf("phone: %d", rec.Code)
	}

	loggedIn, removed := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	loginTab, removeTab := s.tab(), s.tab()
	go func() { loggedIn <- loginTab.post(login, url.Values{"code": {"12345"}}) }()
	<-held.entered // the source is confirming the code
	go func() { removed <- removeTab.post(fmt.Sprintf("/sources/%d/remove", acc), nil) }()
	select {
	case <-removed:
		t.Error("the removal finished while a login was still confirming its code")
		removed <- nil
	case <-time.After(300 * time.Millisecond):
	}
	close(held.release)

	if rec := <-loggedIn; !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=login", acc)) {
		t.Errorf("the login: %d", rec.Code)
	}
	if rec := <-removed; rec != nil && !redirectsTo(rec, "/sources?saved=removed") {
		t.Errorf("the removal: %d", rec.Code)
	}
	if v, _ := s.store.StateGet(acc, "session"); v != nil {
		t.Errorf("the removed source's login is still stored: %q", v)
	}
	if got, _ := s.store.Account(acc); got.Enabled || got.RemovedAt.IsZero() {
		t.Errorf("after removal the source is %+v", got)
	}
}

// Two sources logging in at once share the store of logins in progress: one
// source's step finishing while the other's page is read must not race. Run
// with -race; nothing orders the two requests.
func TestLoginsOfTwoSourcesAtOnce(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	held, h := s.heldAccount("phone")
	other := s.fakeAccount("Personal")
	login := fmt.Sprintf("/sources/%d/login", held)
	s.page(login)

	var wg sync.WaitGroup
	heldTab, otherTab := s.tab(), s.tab()
	wg.Go(func() {
		if rec := heldTab.post(login, url.Values{"phone": {"+1 555"}}); !redirectsTo(rec, login) {
			t.Errorf("phone: %d", rec.Code)
		}
	})
	<-h.entered
	wg.Go(func() {
		if rec := otherTab.get(fmt.Sprintf("/sources/%d/login", other)); rec.Code != http.StatusOK {
			t.Errorf("the other source's login: %d", rec.Code)
		}
	})
	close(h.release)
	wg.Wait()
}

func TestSecretFieldsSayUnchangedOnlyWhenSomethingIsKept(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	login := fmt.Sprintf("/sources/%d/login", acc)
	s.page(login)
	s.post(login, url.Values{"phone": {"+1 555"}})
	if body := s.page(login); !strings.Contains(body, `name="password" type="password"`) || strings.Contains(body, "Unchanged") {
		t.Error("a login step's secret field claims something is kept")
	}
	panel := fmt.Sprintf("/sources/%d", acc)
	if strings.Contains(s.page(panel), "Unchanged") {
		t.Error("an empty secret setting claims something is kept")
	}
	if err := s.store.UpdateAccountSettings(acc, map[string]string{"token": "s3cret"}); err != nil {
		t.Fatal(err)
	}
	if body := s.page(panel); !strings.Contains(body, `placeholder="Unchanged"`) || strings.Contains(body, "s3cret") {
		t.Error("a kept secret setting is not marked as kept, or is shown")
	}
}

func TestTheWelcomePageOffersEachKindOfSource(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	s.reg.Register(connector.Info{Name: "telegram", Title: "Telegram", Display: connector.DisplayChats}, func() connector.Connector { return fakeConnector{} })
	s.reg.Register(connector.Info{Name: "hidden", Title: "Hidden", Display: connector.DisplayChats, Hidden: true}, func() connector.Connector { return fakeConnector{} })

	body := s.page("/")
	has(t, body, "No sources yet", `href="/sources/new?connector=telegram"`, "Telegram",
		"Private chats and groups, with their photos, edits and deletions.",
		"Stays on this computer", `<span class="code">arc5 demo --data &lt;folder&gt;</span>`)
	lacks(t, body, "Hidden", `connector=hidden`)
}

func TestAddingASourceStartsWithTheChosenKind(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	s.reg.Register(connector.Info{Name: "fake", Title: "Fake", Display: connector.DisplayChats}, func() connector.Connector { return fakeConnector{} })
	s.reg.Register(connector.Info{Name: "telegram", Title: "Telegram", Display: connector.DisplayChats}, func() connector.Connector { return fakeConnector{} })

	has(t, s.page("/sources/new?connector=telegram"), `value="telegram" checked`)
	lacks(t, s.page("/sources/new?connector=telegram"), `value="fake" checked`)
	// An unknown kind falls back to the first, as no choice does.
	has(t, s.page("/sources/new?connector=nope"), `value="fake" checked`)
	has(t, s.page("/sources/new"), `value="fake" checked`)
}
