package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fenelon/arc5/internal/model"
)

// UpsertFolder records a mailbox folder. A folder seen for the first time is
// archived if the connector says so; later upserts refresh its name and role
// and never change the owner's choice.
func (s *Store) UpsertFolder(accountID int64, f model.Folder) error {
	if f.SourceID == "" {
		return fmt.Errorf("%w: folder needs a source ID", ErrInvalid)
	}
	if !f.Role.Valid() {
		return fmt.Errorf("%w: folder role %q", ErrInvalid, f.Role)
	}
	name := clean(f.Name)
	if name == "" {
		name = clean(f.SourceID)
	}
	_, err := s.w.Exec(`
		INSERT INTO folders (account_id, source_id, name, role, archived, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, source_id) DO UPDATE SET name = excluded.name, role = excluded.role`,
		accountID, f.SourceID, name, string(f.Role), b2i(f.ArchiveByDefault), s.now().Unix())
	return err
}

// FolderArchived reports whether a folder is being archived. An unknown
// folder is not.
func (s *Store) FolderArchived(accountID int64, folderSourceID string) (bool, error) {
	var archived int
	err := s.r.QueryRow(`SELECT archived FROM folders WHERE account_id = ? AND source_id = ?`, accountID, folderSourceID).Scan(&archived)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return archived != 0, err
}

// FolderEntry is one folder of a mailbox.
type FolderEntry struct {
	ID       int64
	SourceID string
	Name     string
	Role     model.FolderRole
	Archived bool
	Messages int // messages filed there and not gone from it
}

// folderOrder sorts folders as a mail client lists them: the Inbox, Sent
// and All Mail, the owner's own folders by name, then the special ones that
// are not archived by default.
const folderOrder = `CASE role WHEN 'inbox' THEN 0 WHEN 'sent' THEN 1 WHEN 'all' THEN 2 WHEN 'other' THEN 3
	WHEN 'flagged' THEN 4 WHEN 'important' THEN 5 WHEN 'drafts' THEN 6 WHEN 'junk' THEN 7 ELSE 8 END`

// AccountFolders lists every folder of a mailbox, archived or not.
func (s *Store) AccountFolders(accountID int64) ([]FolderEntry, error) {
	rows, err := s.r.Query(`
		SELECT f.id, f.source_id, f.name, f.role, f.archived,
		       (SELECT COUNT(*) FROM message_folders mf WHERE mf.folder_id = f.id AND mf.gone_at IS NULL)
		FROM folders f WHERE f.account_id = ?
		ORDER BY `+folderOrder+`, f.name COLLATE NOCASE, f.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FolderEntry
	for rows.Next() {
		var (
			f        FolderEntry
			role     string
			archived int
		)
		if err := rows.Scan(&f.ID, &f.SourceID, &f.Name, &role, &archived, &f.Messages); err != nil {
			return nil, err
		}
		f.Role, f.Archived = model.FolderRole(role), archived != 0
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetFoldersArchived switches archiving on or off for folders of one
// account, by folder ID. IDs of other accounts' folders are ignored.
func (s *Store) SetFoldersArchived(accountID int64, archived map[int64]bool) error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		for id, on := range archived {
			if _, err := tx.Exec(`UPDATE folders SET archived = ? WHERE id = ? AND account_id = ?`, b2i(on), id, accountID); err != nil {
				return err
			}
		}
		return nil
	})
}

func folderID(q queryer, accountID int64, folderSourceID string) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM folders WHERE account_id = ? AND source_id = ?`, accountID, folderSourceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("folder %q: %w", folderSourceID, ErrNotFound)
	}
	return id, err
}

// FileInFolder records that a message is in a folder at the source, under
// this UID. A message that was marked deleted at source is in a folder
// again, so it is no longer marked.
func (s *Store) FileInFolder(accountID int64, folderSourceID string, uid uint32, chatSourceID, messageSourceID string) error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		folder, err := folderID(tx, accountID, folderSourceID)
		if err != nil {
			return err
		}
		var message int64
		err = tx.QueryRow(`
			SELECT m.id FROM messages m JOIN chats c ON c.id = m.chat_id
			WHERE c.account_id = ? AND c.source_id = ? AND m.source_id = ?`, accountID, chatSourceID, messageSourceID).Scan(&message)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("message %q in %q: %w", messageSourceID, chatSourceID, ErrNotFound)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO message_folders (message_id, folder_id, uid) VALUES (?, ?, ?)
			ON CONFLICT (message_id, folder_id) DO UPDATE SET uid = excluded.uid, gone_at = NULL`, message, folder, uid); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE messages SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL`, message)
		return err
	})
}

// FolderUIDs lists the UIDs of the messages filed in a folder and not gone
// from it, ascending. Messages whose UID was forgotten by ResetFolder are
// left out until they are filed again.
func (s *Store) FolderUIDs(accountID int64, folderSourceID string) ([]uint32, error) {
	folder, err := folderID(s.r, accountID, folderSourceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.r.Query(`SELECT uid FROM message_folders WHERE folder_id = ? AND gone_at IS NULL AND uid > 0 ORDER BY uid`, folder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

// LeaveFolder records that the messages under these UIDs left a folder at
// the source. A message gone from every folder it was filed in is deleted at
// source; it keeps the first time it was marked. Unknown UIDs are ignored.
func (s *Store) LeaveFolder(accountID int64, folderSourceID string, uids []uint32) error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		folder, err := folderID(tx, accountID, folderSourceID)
		if err != nil {
			return err
		}
		now := s.now().Unix()
		for _, uid := range uids {
			var message int64
			err := tx.QueryRow(`SELECT message_id FROM message_folders WHERE folder_id = ? AND uid = ? AND uid > 0 AND gone_at IS NULL`, folder, uid).Scan(&message)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE message_folders SET gone_at = ? WHERE message_id = ? AND folder_id = ?`, now, message, folder); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				UPDATE messages SET deleted_at = ?
				WHERE id = ? AND deleted_at IS NULL
				  AND NOT EXISTS (SELECT 1 FROM message_folders WHERE message_id = ? AND gone_at IS NULL)`, now, message, message); err != nil {
				return err
			}
		}
		return nil
	})
}

// ResetFolder forgets the UIDs of a folder's messages, after the source
// renumbered it (a new UIDVALIDITY). The messages stay filed there until the
// connector has read the folder again.
func (s *Store) ResetFolder(accountID int64, folderSourceID string) error {
	folder, err := folderID(s.r, accountID, folderSourceID)
	if err != nil {
		return err
	}
	_, err = s.w.Exec(`UPDATE message_folders SET uid = 0 WHERE folder_id = ?`, folder)
	return err
}

// SetAccountAddress records a mailbox's own address.
func (s *Store) SetAccountAddress(accountID int64, address string) error {
	return s.updateAccount(accountID, `UPDATE accounts SET address = ? WHERE id = ?`, clean(address), accountID)
}

// Original opens the message as received, for a connector that needs to read
// it again, such as to take out an attachment.
func (s *Store) Original(accountID int64, chatSourceID, messageSourceID string) (io.ReadCloser, error) {
	var sha string
	err := s.r.QueryRow(`
		SELECT m.original_sha256 FROM messages m JOIN chats c ON c.id = m.chat_id
		WHERE c.account_id = ? AND c.source_id = ? AND m.source_id = ?`, accountID, chatSourceID, messageSourceID).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(sha) != 64) {
		return nil, fmt.Errorf("original of %q in %q: %w", messageSourceID, chatSourceID, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return os.Open(s.MediaPath(sha))
}
