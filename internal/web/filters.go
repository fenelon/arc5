package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/fenelon/arc5/internal/search"
)

// Filters narrow a message search. They travel in the address, so every
// link from the results keeps them.
type Filters struct {
	Newest  bool   // newest first instead of best match
	Files   bool   // only messages with attachments
	Deleted bool   // only messages deleted at the source
	Service bool   // service messages too: joins, calls, pins
	From    string // first day, YYYY-MM-DD, or ""
	To      string // last day, YYYY-MM-DD, or ""
}

// FilterBox is the sort switch and the filter form above the results.
type FilterBox struct {
	Filters
	Action, Query                   string
	Active                          int // filters set, not counting the sort
	BestHref, NewestHref, ClearHref string
}

func readFilters(r *http.Request, loc *time.Location) Filters {
	q := r.URL.Query()
	f := Filters{Newest: q.Get("sort") == "newest", Files: q.Get("files") == "1", Deleted: q.Get("deleted") == "1", Service: q.Get("service") == "1"}
	if _, err := time.ParseInLocation(time.DateOnly, q.Get("from"), loc); err == nil {
		f.From = q.Get("from")
	}
	if _, err := time.ParseInLocation(time.DateOnly, q.Get("to"), loc); err == nil {
		f.To = q.Get("to")
	}
	return f
}

// suffix is the filters as address parameters, each starting with "&".
func (f Filters) suffix() string {
	s := ""
	if f.Newest {
		s += "&sort=newest"
	}
	return s + f.narrowing()
}

// narrowing is the filters other than the sort, as address parameters.
func (f Filters) narrowing() string {
	s := ""
	if f.Files {
		s += "&files=1"
	}
	if f.Deleted {
		s += "&deleted=1"
	}
	if f.Service {
		s += "&service=1"
	}
	if f.From != "" {
		s += "&from=" + url.QueryEscape(f.From)
	}
	if f.To != "" {
		s += "&to=" + url.QueryEscape(f.To)
	}
	return s
}

func (f Filters) active() int {
	n := 0
	for _, on := range []bool{f.Files, f.Deleted, f.Service, f.From != "", f.To != ""} {
		if on {
			n++
		}
	}
	return n
}

// apply sets the filters on a search. The last day is included whole.
func (f Filters) apply(q *search.Query, loc *time.Location) {
	if f.Newest {
		q.Sort = search.ByNewest
	}
	q.HasAttachment, q.DeletedOnly, q.IncludeService = f.Files, f.Deleted, f.Service
	if t, err := time.ParseInLocation(time.DateOnly, f.From, loc); err == nil {
		q.From = t
	}
	if t, err := time.ParseInLocation(time.DateOnly, f.To, loc); err == nil {
		q.To = t.AddDate(0, 0, 1)
	}
}

// box is the filter form for a search whose plain address is base ("?q=…").
func (f Filters) box(action, query, base string) *FilterBox {
	b := &FilterBox{Filters: f, Action: action, Query: query, Active: f.active()}
	b.BestHref = base + f.narrowing()
	b.NewestHref = base + "&sort=newest" + f.narrowing()
	b.ClearHref = base
	if f.Newest {
		b.ClearHref += "&sort=newest"
	}
	return b
}
