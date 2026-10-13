package web

import (
	"net/http"
)

// ThreadView is an open mail conversation.
type ThreadView struct {
	Title string
}

// threadView reads a mail conversation for its page. It reports false when
// the conversation is not in the selection.
func (s *Server) threadView(r *http.Request, sel selection, folder mailFolder, chatID int64) (*ThreadView, bool, error) {
	return nil, false, nil
}
