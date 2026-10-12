// Package web serves arc5's HTTP interface.
package web

import (
	"io"
	"net/http"
)

// NewHandler returns the HTTP handler. /healthz is unauthenticated and
// deliberately says nothing about accounts.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`+"\n")
	})
	return mux
}
