package web

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/archive"
)

// avatarURL is where a stored picture is served, or "" for none.
func avatarURL(sha string) string {
	if sha == "" {
		return ""
	}
	return "/avatars/" + sha
}

// avatarTypes are the picture types served as avatars. Anything else stored
// as a picture, such as SVG, is not served at all.
var avatarTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

// serveAvatar sends a chat's or person's current picture. Pictures are kept
// by content hash, so an address never changes what it shows.
func (s *Server) serveAvatar(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	sha := r.PathValue("sha")
	if len(sha) != 64 || strings.Trim(sha, "0123456789abcdef") != "" || !s.deps.Store.IsAvatar(sha) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(s.deps.Store.MediaPath(sha))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		s.fail(w, err)
		return
	}
	contentType := http.DetectContentType(head[:n])
	if !avatarTypes[contentType] {
		http.NotFound(w, r)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.fail(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", mediaPolicy)
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, f)
}
