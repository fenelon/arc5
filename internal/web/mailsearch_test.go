package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMailSearch(t *testing.T) {
	t.Parallel()
	m := newMailSite(t)
	planning := m.thread("Monday planning, room 4")
	body := m.page(fmt.Sprintf("/s/%d?q=projector", m.box))
	has(t, body, `<nav class="folders" aria-label="Folders">`, `<section class="mailmain" id="list" aria-label="Search results">`,
		`<input class="search__input" type="search" name="q" value="projector"`,
		`<mark class="hit">projector</mark>`, "Monday planning, room 4",
		fmt.Sprintf(`href="/s/%d/c/%d?at=`, m.box, planning))
	lacks(t, body, "Joins, calls and pins too", `<h1 class="mail-head__title">`)

	// Subjects are searched, and conversations are named as such.
	subject := m.page(fmt.Sprintf("/s/%d?q=renewal", m.box))
	has(t, subject, "Conversations and people", "Membership renewal reminder", "Conversation, last message")
	lacks(t, subject, "Chats and people")

	// People by name or address.
	people := m.page(fmt.Sprintf("/s/%d?q=%s", m.box, url.QueryEscape("priya.nair")))
	has(t, people, `<mark class="hit">Priya</mark>`, "Everything from this person, in any conversation")

	// The search controller's fragment is the main column's content.
	rec := m.get(fmt.Sprintf("/s/%d/results?q=projector", m.box))
	if rec.Code != http.StatusOK {
		t.Fatalf("fragment: %d", rec.Code)
	}
	frag := rec.Body.String()
	has(t, frag, `<mark class="hit">projector</mark>`)
	lacks(t, frag, "<html", `<nav class="folders"`)
	empty := m.get(fmt.Sprintf("/s/%d/results?q=", m.box)).Body.String()
	has(t, empty, `<h1 class="mail-head__title">Inbox</h1>`, "Dinner on Saturday")
}

func TestChooseFolders(t *testing.T) {
	t.Parallel()
	m := newMailSite(t)
	page := m.page(fmt.Sprintf("/sources/%d/chats", m.box))
	has(t, page, "<h1>Folders of Home mail</h1>", "Ticked folders are archived.",
		fmt.Sprintf(`name="archived" value="%d" checked`, m.folder("Inbox")),
		fmt.Sprintf(`name="archived" value="%d">`, m.folder("Trash")), "Receipts", "Trash")
	lacks(t, page, "Chats of Home mail", "Monday planning")

	panel := m.page(fmt.Sprintf("/sources/%d", m.box))
	has(t, panel, "Choose which of the 4 folders are archived", ">Open its mail</a>", "On Gmail, Inbox, Sent and All Mail")
	lacks(t, panel, "Open its chats")
	lacks(t, panel, "chats are archived")

	form := url.Values{"csrf": {m.csrf}}
	for _, name := range []string{"Inbox", "Sent", "Receipts", "Trash"} {
		form.Add("shown", fmt.Sprint(m.folder(name)))
	}
	form.Add("archived", fmt.Sprint(m.folder("Inbox")))
	form.Add("archived", fmt.Sprint(m.folder("Trash")))
	rec := m.post(fmt.Sprintf("/sources/%d/chats", m.box), form)
	if !redirectsTo(rec, fmt.Sprintf("/sources/%d/chats?saved=1", m.box)) {
		t.Fatalf("save: %d %v", rec.Code, rec.Header())
	}
	folders, _ := m.store.AccountFolders(m.box)
	var on []string
	for _, f := range folders {
		if f.Archived {
			on = append(on, f.Name)
		}
	}
	if strings.Join(on, ",") != "Inbox,Trash" {
		t.Errorf("archived folders = %v", on)
	}
	if !slicesContains(m.runner.restarted, m.box) {
		t.Error("the source was not restarted")
	}
}

func slicesContains(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
