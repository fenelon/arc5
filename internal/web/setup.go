package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
)

// flowLifetime is how long an unfinished login waits for its next step.
const flowLifetime = 30 * time.Minute

// flow is a connector's setup flow in progress for one account. Its mutex
// keeps two submissions of the same step from running at once.
type flow struct {
	mu      sync.Mutex
	setup   connector.SetupFlow
	step    connector.Step
	expires time.Time
}

// flows holds the setup flows in progress, one per account.
type flows struct {
	mu     sync.Mutex
	byAcct map[int64]*flow
}

// end closes a flow if it holds anything open.
func end(f *flow) {
	if c, ok := f.setup.(io.Closer); ok {
		c.Close()
	}
}

// get returns the account's flow, unless it has expired. Expired flows of
// any account are closed on the way.
func (fs *flows) get(accountID int64, now time.Time) *flow {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for id, f := range fs.byAcct {
		if now.After(f.expires) {
			delete(fs.byAcct, id)
			end(f)
		}
	}
	return fs.byAcct[accountID]
}

func (fs *flows) put(accountID int64, f *flow) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.byAcct == nil {
		fs.byAcct = map[int64]*flow{}
	}
	if old := fs.byAcct[accountID]; old != nil && old != f {
		end(old)
	}
	fs.byAcct[accountID] = f
}

// drop ends the account's flow, if any.
func (fs *flows) drop(accountID int64) {
	fs.mu.Lock()
	f := fs.byAcct[accountID]
	delete(fs.byAcct, accountID)
	fs.mu.Unlock()
	if f != nil {
		end(f)
	}
}

// ConnectorChoice is a kind of source the owner can add.
type ConnectorChoice struct {
	Name, Title, Logo string
	Checked           bool
}

type addPage struct {
	Page
	Choices []ConnectorChoice
	Name    string
	Problem string
}

func (s *Server) addable() []ConnectorChoice {
	var out []ConnectorChoice
	for _, info := range s.deps.Registry.Infos() {
		if !info.Hidden {
			out = append(out, ConnectorChoice{Name: info.Name, Title: info.Title, Logo: logoFor(info.Name)})
		}
	}
	return out
}

func (s *Server) addForm(w http.ResponseWriter, r *http.Request, sess archive.Session, status int, chosen, name, problem string) {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	data := addPage{Page: s.chrome(r, sess, "Add a source", all, ""), Choices: s.addable(), Name: name, Problem: problem}
	for i := range data.Choices {
		data.Choices[i].Checked = data.Choices[i].Name == chosen || (chosen == "" && i == 0)
	}
	s.render(w, status, "add", data)
}

func (s *Server) newSourceForm(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	s.addForm(w, r, sess, http.StatusOK, "", "", "")
}

// addSource creates the account switched off, then runs its connector's
// login. The account is switched on when the login finishes.
func (s *Server) addSource(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	name, chosen := strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("connector")
	info, known := s.deps.Registry.Info(chosen)
	switch {
	case !known || info.Hidden:
		s.addForm(w, r, sess, http.StatusUnprocessableEntity, "", name, "Choose one of the kinds of source listed.")
		return
	case name == "":
		s.addForm(w, r, sess, http.StatusUnprocessableEntity, chosen, name, "Give the source a name, such as Personal or Work.")
		return
	}
	id, err := s.deps.Store.CreateAccount(chosen, name, nil)
	if err == nil {
		err = s.deps.Store.SetAccountEnabled(id, false)
	}
	if err == nil {
		err = s.deps.Store.RenameAccount(id, name) // tidies the name as a rename would
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d/login", id))
}

type loginPage struct {
	Page
	AccountID int64
	Name      string
	StepTitle string // the step's own title; Page.Title names the page
	Help      string
	Fields    []FieldView
	Problem   string
}

// startFlow begins an account's login, or returns false and answers the
// request itself when its connector has none.
func (s *Server) startFlow(w http.ResponseWriter, r *http.Request, acc archive.Account) (*flow, bool) {
	c, ok := s.newConnector(acc.Connector)
	if !ok {
		http.NotFound(w, r)
		return nil, false
	}
	setup := c.Setup(connector.NewState(s.deps.Store, acc.ID))
	if setup == nil {
		http.NotFound(w, r)
		return nil, false
	}
	f := &flow{setup: setup, expires: s.deps.Now().Add(flowLifetime)}
	step, err := setup.Next(r.Context(), nil)
	if err != nil {
		if c, ok := setup.(io.Closer); ok {
			c.Close()
		}
		s.fail(w, fmt.Errorf("starting the login of account %d: %w", acc.ID, err))
		return nil, false
	}
	f.step = step
	s.flows.put(acc.ID, f)
	return f, true
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, sess archive.Session, acc archive.Account, step connector.Step, status int, problem string) {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	data := loginPage{Page: s.chrome(r, sess, "Log in to "+acc.Label, all, ""), AccountID: acc.ID, Name: acc.Label,
		StepTitle: step.Title, Help: step.Help, Problem: problem}
	for _, f := range step.Fields {
		v := FieldView{ID: "step-" + f.Name, Name: f.Name, Label: f.Label, Help: f.Help, Kind: string(f.Kind), Value: f.Default}
		if f.Kind == connector.FieldSecret {
			v.Value = ""
		}
		for _, choice := range f.Choices {
			v.Choices = append(v.Choices, Choice{Value: choice, Selected: choice == f.Default})
		}
		data.Fields = append(data.Fields, v)
	}
	s.render(w, status, "login-step", data)
}

func (s *Server) loginStep(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	f := s.flows.get(acc.ID, s.deps.Now())
	if f == nil {
		if f, ok = s.startFlow(w, r, acc); !ok {
			return
		}
	}
	f.mu.Lock()
	step := f.step
	f.mu.Unlock()
	s.renderLogin(w, r, sess, acc, step, http.StatusOK, "")
}

// submitStep sends a step's answers to the connector. A refused answer shows
// the same step again with the connector's explanation.
func (s *Server) submitStep(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	login := fmt.Sprintf("/sources/%d/login", acc.ID)
	f := s.flows.get(acc.ID, s.deps.Now())
	if f == nil {
		if _, ok := s.startFlow(w, r, acc); ok {
			redirect(w, r, login) // nothing was asked yet; show the first step
		}
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	input := map[string]string{}
	for _, field := range f.step.Fields {
		input[field.Name] = r.PostFormValue(field.Name)
	}
	next, err := f.setup.Next(r.Context(), input)
	if err != nil {
		s.renderLogin(w, r, sess, acc, f.step, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if !next.Done {
		f.step, f.expires = next, s.deps.Now().Add(flowLifetime)
		redirect(w, r, login)
		return
	}
	s.flows.drop(acc.ID)
	if err := s.deps.Store.SetAccountEnabled(acc.ID, true); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d?saved=login", acc.ID))
}

func (s *Server) cancelLogin(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	acc, ok := s.account(w, r)
	if !ok {
		return
	}
	s.flows.drop(acc.ID)
	redirect(w, r, fmt.Sprintf("/sources/%d", acc.ID))
}
