package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Where the bot's token lives. It is a secret, so it is among the secrets: no
// API returns it, and unlike the settings it is not copied to the nodes — the
// bot runs on the master alone.
const (
	telegramTokenKey    = "telegram_token"
	telegramUsernameKey = "telegram_username"
)

// LinkCodeTTL is how long a link stays good. It is handed to a customer through
// whatever channel the operator already uses with them, so it has to outlast
// the time they take to open it; it is single-use, so a longer life costs
// little.
const LinkCodeTTL = 7 * 24 * time.Hour

// TelegramToken returns the bot's token and username, both "" when no bot has
// been set up. The username is what a link to the bot is built from.
func (s *Store) TelegramToken() (token, username string, err error) {
	t, _, err := s.Secret(telegramTokenKey)
	if err != nil {
		return "", "", err
	}
	u, _, err := s.Secret(telegramUsernameKey)
	return string(t), string(u), err
}

// SetTelegramToken stores the bot's token with the username Telegram reported
// for it. An empty token removes the bot altogether.
func (s *Store) SetTelegramToken(token, username string) error {
	if token == "" {
		if err := s.DeleteSecret(telegramTokenKey); err != nil {
			return err
		}
		return s.DeleteSecret(telegramUsernameKey)
	}
	if err := s.PutSecret(telegramTokenKey, []byte(token)); err != nil {
		return err
	}
	return s.PutSecret(telegramUsernameKey, []byte(username))
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// CreateLinkCode makes the code that connects a chat to a peer. It is
// unguessable and fits in the 64 characters Telegram allows after /start.
//
// A peer has at most one outstanding code: asking for another retires the first,
// so a link that was sent to the wrong person stops working when the right one
// is made. Only a hash is stored.
func (s *Store) CreateLinkCode(peerID string) (code string, expiresAt int64, err error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, err
	}
	code = base64.RawURLEncoding.EncodeToString(raw)
	expiresAt = time.Now().Add(LinkCodeTTL).UnixMilli()

	tx, err := s.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM telegram_codes WHERE peer_id = ?`, peerID); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO telegram_codes (code_hash, peer_id, expires_at) VALUES (?, ?, ?)`,
		hashCode(code), peerID, expiresAt); err != nil {
		return "", 0, err
	}
	return code, expiresAt, tx.Commit()
}

// RedeemLinkCode connects chatID to the peer the code was made for and returns
// the peer's name. ErrNotFound covers every way a code can be no good — never
// issued, already used, expired — because the person holding it needs no more
// precise an answer and an attacker should not get one.
func (s *Store) RedeemLinkCode(code string, chatID int64) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	at := now()
	if _, err := tx.Exec(`DELETE FROM telegram_codes WHERE expires_at <= ?`, at); err != nil {
		return "", err
	}

	hash := hashCode(code)
	var peerID, name string
	err = tx.QueryRow(`SELECT c.peer_id, p.name FROM telegram_codes c
	                   JOIN peers p ON p.id = c.peer_id WHERE c.code_hash = ?`, hash).Scan(&peerID, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}

	// Single use: spent whether or not anything else goes wrong.
	if _, err := tx.Exec(`DELETE FROM telegram_codes WHERE code_hash = ?`, hash); err != nil {
		return "", err
	}
	// A chat that has just been connected has been told nothing, so what the
	// peer was warned about before is forgotten: otherwise a customer who moved
	// to a new phone, or blocked the bot and came back, would hear nothing about
	// running low until the situation changed.
	if _, err := tx.Exec(`DELETE FROM telegram_warned WHERE peer_id = ?`, peerID); err != nil {
		return "", err
	}
	// Connecting again replaces the chat, which is how someone moves to a new
	// phone.
	if _, err := tx.Exec(`
	    INSERT INTO telegram_links (peer_id, chat_id, linked_at) VALUES (?, ?, ?)
	    ON CONFLICT (peer_id) DO UPDATE SET chat_id = excluded.chat_id, linked_at = excluded.linked_at`,
		peerID, chatID, at); err != nil {
		return "", err
	}
	return name, tx.Commit()
}

// TelegramLinked reports whether warnings for the peer go to a chat.
func (s *Store) TelegramLinked(peerID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM telegram_links WHERE peer_id = ?`, peerID).Scan(&n)
	return n > 0, err
}

// UnlinkPeer stops warnings for the peer and retires any code still out for it.
func (s *Store) UnlinkPeer(peerID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM telegram_links WHERE peer_id = ?`, peerID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM telegram_codes WHERE peer_id = ?`, peerID); err != nil {
		return err
	}
	return tx.Commit()
}

// UnlinkChat drops every peer connected to a chat, for when Telegram says the
// chat can no longer be written to — the customer blocked the bot. It returns
// how many peers were dropped.
func (s *Store) UnlinkChat(chatID int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM telegram_links WHERE chat_id = ?`, chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountTelegramLinks is how many peers are connected to a chat.
func (s *Store) CountTelegramLinks() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM telegram_links`).Scan(&n)
	return n, err
}

// Standing is where one connected peer stands against its limits.
type Standing struct {
	PeerID string
	Name   string
	ChatID int64
	Status string
	// Allowed, Used and ExpiresAt are the limits in force, which for a group
	// member are its group's: that is what will cut it off, and so what a
	// warning has to be about. 0 means no limit.
	Allowed   int64
	Used      int64
	ExpiresAt int64
	// ResetAt and GroupID say what Used is counted from, so a reset or a move
	// to another group reads as a new situation rather than the old one going on.
	ResetAt int64
	GroupID int64
	// GroupName and OwnUsed are for telling a customer where they stand. A
	// member's Used is the whole group's, which is what cuts it off; what the
	// customer asks about is how much of it is theirs.
	GroupName string
	OwnUsed   int64
}

// Standings returns every peer that is connected to a chat.
func (s *Store) Standings(now int64) ([]Standing, error) {
	return s.standings(now, "")
}

// ChatStandings returns the peers connected to one chat.
func (s *Store) ChatStandings(now, chatID int64) ([]Standing, error) {
	return s.standings(now, "WHERE l.chat_id = ?", chatID)
}

func (s *Store) standings(now int64, where string, args ...any) ([]Standing, error) {
	rows, err := s.db.Query(peerCTE+`
	    SELECT v.id, v.name, l.chat_id, v.status,
	           v.eff_allowed_usage, v.eff_usage, v.eff_expires_at, v.usage_reset_at, v.group_id,
	           v.group_name, v.usage
	    FROM v JOIN telegram_links l ON l.peer_id = v.id `+where+`
	    ORDER BY l.chat_id, v.name COLLATE NOCASE`, append([]any{now, now}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Standing
	for rows.Next() {
		var st Standing
		if err := rows.Scan(&st.PeerID, &st.Name, &st.ChatID, &st.Status,
			&st.Allowed, &st.Used, &st.ExpiresAt, &st.ResetAt, &st.GroupID,
			&st.GroupName, &st.OwnUsed); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Warning kinds.
const (
	WarnExpiry = "expiry"
	WarnUsage  = "usage"
)

// WarnedKey identifies one kind of warning for one peer.
type WarnedKey struct{ PeerID, Kind string }

// Warned returns, for every peer and kind, the situation it was last warned
// about.
func (s *Store) Warned() (map[WarnedKey]string, error) {
	rows, err := s.db.Query(`SELECT peer_id, kind, marker FROM telegram_warned`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[WarnedKey]string{}
	for rows.Next() {
		var k WarnedKey
		var marker string
		if err := rows.Scan(&k.PeerID, &k.Kind, &marker); err != nil {
			return nil, err
		}
		out[k] = marker
	}
	return out, rows.Err()
}

// MarkWarned records that the peer has been warned about a situation, so it is
// not warned about it again.
func (s *Store) MarkWarned(peerID, kind, marker string) error {
	_, err := s.db.Exec(`
	    INSERT INTO telegram_warned (peer_id, kind, marker, sent_at) VALUES (?, ?, ?, ?)
	    ON CONFLICT (peer_id, kind) DO UPDATE SET marker = excluded.marker, sent_at = excluded.sent_at`,
		peerID, kind, marker, now())
	return err
}
