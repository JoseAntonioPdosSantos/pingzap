package store

import "time"

// RemoteLink is a person's web remote of a bot: whoever opens the link acts
// as that person. A person has at most one link per bot.
type RemoteLink struct {
	ID    int64  `json:"id"`
	BotID int64  `json:"bot_id"`
	Phone string `json:"phone"`
	Name  string `json:"name"`
	// Token is the secret in the link; it never leaves the server except in
	// the link sent to the person.
	Token string `json:"-"`
	// ChatKey and ChatJID are the chat where the link was last asked for,
	// which is told about every use.
	ChatKey   string     `json:"-"`
	ChatJID   string     `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	Uses      int        `json:"uses"`
}

// RemoteUse is one action taken through a web remote.
type RemoteUse struct {
	Phone   string    `json:"phone"`
	Name    string    `json:"name"`
	Action  string    `json:"action"`
	Command string    `json:"command"`
	State   string    `json:"state"`
	At      time.Time `json:"at"`
}

const remoteLinkColumns = `id, bot_id, phone, name, token, chat_key, chat_jid, created_at, last_used, uses`

func scanRemoteLink(row interface{ Scan(...any) error }) (RemoteLink, error) {
	var l RemoteLink
	var created, lastUsed int64
	err := row.Scan(&l.ID, &l.BotID, &l.Phone, &l.Name, &l.Token, &l.ChatKey, &l.ChatJID, &created, &lastUsed, &l.Uses)
	l.CreatedAt = time.Unix(created, 0)
	if lastUsed > 0 {
		t := time.Unix(lastUsed, 0)
		l.LastUsed = &t
	}
	return l, err
}

// EnsureRemoteLink returns the person's link for the bot, creating it on the
// first request. Later requests keep the token and only refresh the name and
// the chat to tell about each use.
func (s *Store) EnsureRemoteLink(botID int64, phone, name, chatKey, chatJID string) (RemoteLink, error) {
	if phone == "" {
		return RemoteLink{}, invalid("número não identificado")
	}
	_, err := s.db.Exec(`
		INSERT INTO remote_links (bot_id, phone, name, token, chat_key, chat_jid, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (bot_id, phone) DO UPDATE SET
			name = excluded.name, chat_key = excluded.chat_key, chat_jid = excluded.chat_jid`,
		botID, phone, name, randomToken(), chatKey, chatJID, time.Now().Unix())
	if err != nil {
		return RemoteLink{}, err
	}
	return scanRemoteLink(s.db.QueryRow(
		`SELECT `+remoteLinkColumns+` FROM remote_links WHERE bot_id = ? AND phone = ?`, botID, phone))
}

func (s *Store) RemoteLinkByToken(token string) (RemoteLink, error) {
	if token == "" {
		return RemoteLink{}, ErrNotFound
	}
	l, err := scanRemoteLink(s.db.QueryRow(`SELECT `+remoteLinkColumns+` FROM remote_links WHERE token = ?`, token))
	return l, notFound(err)
}

func (s *Store) RemoteLinks(botID int64) ([]RemoteLink, error) {
	rows, err := s.db.Query(`SELECT `+remoteLinkColumns+` FROM remote_links WHERE bot_id = ? ORDER BY name, phone`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []RemoteLink{}
	for rows.Next() {
		l, err := scanRemoteLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// DeleteRemoteLink revokes a link: it stops working, and the person gets a
// new one the next time they ask.
func (s *Store) DeleteRemoteLink(botID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM remote_links WHERE id = ? AND bot_id = ?`, id, botID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LogRemoteUse records an action taken through a link. The history keeps
// the person's name and number, so it outlives the link.
func (s *Store) LogRemoteUse(l RemoteLink, u RemoteUse) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := u.At.Unix()
	if _, err := tx.Exec(`
		INSERT INTO remote_uses (bot_id, phone, name, action, command, state, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		l.BotID, l.Phone, l.Name, u.Action, u.Command, u.State, at); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE remote_links SET last_used = ?, uses = uses + 1 WHERE id = ?`, at, l.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoteUses returns the latest actions taken through the bot's links,
// newest first.
func (s *Store) RemoteUses(botID int64, limit int) ([]RemoteUse, error) {
	rows, err := s.db.Query(`
		SELECT phone, name, action, command, state, at FROM remote_uses
		WHERE bot_id = ? ORDER BY at DESC, id DESC LIMIT ?`, botID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uses := []RemoteUse{}
	for rows.Next() {
		var u RemoteUse
		var at int64
		if err := rows.Scan(&u.Phone, &u.Name, &u.Action, &u.Command, &u.State, &at); err != nil {
			return nil, err
		}
		u.At = time.Unix(at, 0)
		uses = append(uses, u)
	}
	return uses, rows.Err()
}
