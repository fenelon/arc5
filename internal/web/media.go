package web

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

// mediaPolicy stops a file from running script or loading anything, even if a
// browser is talked into displaying it.
const mediaPolicy = "sandbox; default-src 'none'"

// serveMedia sends an archived file. Files come from other people, so the
// type they claim is trusted only if it is on a short list of plain media;
// everything else, including HTML and SVG, is a download.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := s.deps.Store.AttachmentFile(id)
	if errors.Is(err, archive.ErrNotFound) || (err == nil && file.Path == "") {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	f, err := os.Open(file.Path)
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "This file is missing from the data folder.", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()

	contentType, disposition := "application/octet-stream", "attachment"
	if inlineKind(file.MimeType) != "" {
		contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(file.MimeType, ";", 2)[0]))
		disposition = "inline"
	}
	name := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, file.FileName)
	if name == "" {
		name = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	// FormatMediaType quotes or encodes the name; it returns "" for one it cannot.
	if withName := mime.FormatMediaType(disposition, map[string]string{"filename": name}); withName != "" {
		disposition = withName
	}
	h.Set("Content-Disposition", disposition)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", mediaPolicy)
	h.Set("Cache-Control", "private, max-age=31536000, immutable") // a stored attachment never changes
	http.ServeContent(w, r, "", time.Time{}, f)
}

// localPath returns back if it is an address on this site, else "/".
func localPath(back string) string {
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.ContainsFunc(back, func(r rune) bool {
		return r == '\\' || r < 0x20 || r == 0x7f // browsers drop tabs and newlines, which would turn "/\t/x" into "//x"
	}) {
		return "/"
	}
	return back
}

// fetchMedia asks the source's connector to download an attachment that was
// skipped or failed, whatever its size.
func (s *Server) fetchMedia(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := s.deps.Store.AttachmentFile(id)
	if err == nil && file.Ephemeral && file.Status == model.StatusSkipped {
		http.Error(w, "Self-destructing media is fetched only when its source is set to keep it.", http.StatusConflict)
		return
	}
	switch err := s.deps.Runner.RequestMedia(id); {
	case errors.Is(err, archive.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, err)
	default:
		redirect(w, r, localPath(r.PostFormValue("back")))
	}
}
