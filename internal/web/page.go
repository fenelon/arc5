package web

// Page is what every signed-in page shows around its own content.
type Page struct {
	Title  string
	Theme  string     // "", "light" or "dark"; empty follows the device
	CSRF   string     // the session's token, for forms
	Picker *Picker    // nil when there are no sources
	Search *SearchBox // nil on pages without a search box
	Alert  *Alert     // nil when every source is archiving
}

// Tile is one source, or "Everything at once", as the picker and the Sources
// page show it.
type Tile struct {
	Name       string // the name the owner gave the source
	Service    string // "Telegram", or a count for "Everything at once"
	Logo       string // "telegram", "whatsapp", "email", "all" or "generic"
	State      string // "live", "catching-up", "needs-login", "offline" or ""
	StateLabel string
	Detail     string // a longer line for the Sources page
	Href       string
	Current    bool
	Warn       bool // drawn with the attention border
}

// PickerGroup is the sources of one display type.
type PickerGroup struct {
	Title string
	Tiles []Tile // "Everything at once" first
}

// Picker is the source picker in the top bar.
type Picker struct {
	Current Tile
	Groups  []PickerGroup
}

// Alert names a source that is not archiving.
type Alert struct {
	Name   string
	Detail string
}
