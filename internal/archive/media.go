package archive

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fenelon/arc5/internal/model"
)

// MediaPath is where a file with this SHA-256 (hex) is kept. Identical files
// share one path, so a forwarded photo is stored once.
func (s *Store) MediaPath(sha256hex string) string {
	return filepath.Join(s.dataDir, "media", sha256hex[:2], sha256hex[2:4], sha256hex)
}

const attachmentByRef = `
	SELECT a.id FROM attachments a
	JOIN messages m ON m.id = a.message_id
	JOIN chats c ON c.id = m.chat_id
	WHERE c.account_id = ? AND c.source_id = ? AND m.source_id = ? AND a.position = ?`

func (s *Store) attachmentID(accountID int64, ref model.AttachmentRef) (int64, error) {
	var id int64
	err := s.w.QueryRow(attachmentByRef, accountID, ref.ChatSourceID, ref.MessageSourceID, ref.Position).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: %+v", ErrUnknownAttachment, ref)
	}
	return id, err
}

// readTracker remembers whether a copy failed on the reading side.
type readTracker struct {
	r   io.Reader
	err error
}

func (t *readTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}

// PutMedia stores an attachment's bytes and marks it stored. The file is
// written to a temp file, synced, and renamed into place, so a failed or
// interrupted download never leaves a partial file under a content hash.
// A failure of r is reported wrapping ErrMediaRead and leaves the attachment
// as it was.
func (s *Store) PutMedia(accountID int64, ref model.AttachmentRef, r io.Reader) error {
	id, err := s.attachmentID(accountID, ref)
	if err != nil {
		return err
	}
	tmpDir := filepath.Join(s.dataDir, "media", "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(tmpDir, "put-*") // created with mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly once the file has been renamed

	hash := sha256.New()
	tracked := &readTracker{r: r}
	size, err := io.Copy(io.MultiWriter(tmp, hash), tracked)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if tracked.err != nil {
		return fmt.Errorf("%w: %v", ErrMediaRead, tracked.err)
	}
	if err != nil {
		return err
	}

	sum := hex.EncodeToString(hash.Sum(nil))
	final := s.MediaPath(sum)
	if _, err := os.Stat(final); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), final); err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(final)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	_, err = s.w.Exec(`UPDATE attachments SET sha256 = ?, size = ?, status = 'stored', reason = '', requested = 0 WHERE id = ?`, sum, size, id)
	return err
}

// syncDir flushes a directory after a rename into it, so the new file's name
// survives a power cut along with its contents. Filesystems that cannot sync
// a directory (some network mounts) report EINVAL or ENOTSUP; that is not an
// error the caller can act on.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if closeErr := d.Close(); err == nil {
		err = closeErr
	}
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}

// SetAttachmentStatus records why an attachment was not stored. Only skipped,
// failed and unavailable are accepted; stored is reached through PutMedia and
// pending through RequestMedia.
func (s *Store) SetAttachmentStatus(accountID int64, ref model.AttachmentRef, status model.AttachmentStatus, reason string) error {
	switch status {
	case model.StatusSkipped, model.StatusFailed, model.StatusUnavailable:
	default:
		return fmt.Errorf("%w: attachment status %q cannot be set directly", ErrInvalid, status)
	}
	id, err := s.attachmentID(accountID, ref)
	if err != nil {
		return err
	}
	// A stored attachment stays stored: a duplicate download that fails later
	// must not hide the copy that was saved.
	_, err = s.w.Exec(`UPDATE attachments SET status = ?, reason = ?, requested = 0 WHERE id = ? AND status != 'stored'`, string(status), clean(reason), id)
	return err
}

// RequestMedia queues an attachment for download regardless of the size cap,
// and returns the account and ref so the caller can notify a running connector.
// The request is stored, so it survives until a connector fulfils it.
func (s *Store) RequestMedia(attachmentID int64) (int64, model.AttachmentRef, error) {
	tx, err := s.w.Begin()
	if err != nil {
		return 0, model.AttachmentRef{}, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE attachments SET status = 'pending', reason = '', requested = 1 WHERE id = ? AND status != 'stored'`, attachmentID)
	if err != nil {
		return 0, model.AttachmentRef{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return 0, model.AttachmentRef{}, err
	} else if n == 0 {
		return 0, model.AttachmentRef{}, fmt.Errorf("attachment %d is missing or already stored: %w", attachmentID, ErrNotFound)
	}

	var (
		accountID int64
		ref       model.AttachmentRef
	)
	err = tx.QueryRow(`
		SELECT c.account_id, c.source_id, m.source_id, a.position FROM attachments a
		JOIN messages m ON m.id = a.message_id
		JOIN chats c ON c.id = m.chat_id
		WHERE a.id = ?`, attachmentID).Scan(&accountID, &ref.ChatSourceID, &ref.MessageSourceID, &ref.Position)
	if err != nil {
		return 0, model.AttachmentRef{}, err
	}
	return accountID, ref, tx.Commit()
}

// RequestedMedia lists an account's attachments waiting on an explicit request.
func (s *Store) RequestedMedia(accountID int64) ([]model.AttachmentRef, error) {
	rows, err := s.r.Query(`
		SELECT c.source_id, m.source_id, a.position FROM attachments a
		JOIN messages m ON m.id = a.message_id
		JOIN chats c ON c.id = m.chat_id
		WHERE a.requested = 1 AND a.status = 'pending' AND c.account_id = ?
		ORDER BY a.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AttachmentRef
	for rows.Next() {
		var ref model.AttachmentRef
		if err := rows.Scan(&ref.ChatSourceID, &ref.MessageSourceID, &ref.Position); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
