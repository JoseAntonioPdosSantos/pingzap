package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const (
	KindFlow  = "flow"
	KindAlert = "alert"

	TargetContact = "contact"
	TargetGroup   = "group"
)

// Bot answers the chats in Targets. Config holds the kind-specific settings
// as JSON (a flow.Flow or an alert configuration); the store does not
// interpret it.
type Bot struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Enabled  bool            `json:"enabled"`
	DeviceID *int64          `json:"device_id"`
	Config   json.RawMessage `json:"config"`
	// CurrentState is the flow state shared by every chat of the bot.
	CurrentState string   `json:"current_state"`
	Targets      []Target `json:"targets"`
}

// Target is a chat that talks to a bot. Value is the normalized phone
// number for contacts and the group JID for groups. A chat belongs to at
// most one bot.
type Target struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Label string `json:"label"`
}

func (b *Bot) validate() error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return invalid("informe o nome do bot")
	}
	if b.Kind != KindFlow && b.Kind != KindAlert {
		return invalid("tipo de bot inválido")
	}
	if len(b.Config) == 0 || !json.Valid(b.Config) {
		return invalid("configuração do bot inválida")
	}
	seen := make(map[Target]bool)
	for _, t := range b.Targets {
		if t.Kind != TargetContact && t.Kind != TargetGroup {
			return invalid("tipo de contato inválido")
		}
		key := Target{Kind: t.Kind, Value: t.Value}
		if t.Value == "" || seen[key] {
			return invalid("contato ou grupo vazio ou repetido")
		}
		seen[key] = true
	}
	return nil
}

func (s *Store) ListBots() ([]Bot, error) {
	rows, err := s.db.Query(`SELECT id, name, kind, enabled, device_id, config, current_state FROM bots ORDER BY name`)
	if err != nil {
		return nil, err
	}
	bots := []Bot{}
	for rows.Next() {
		b, err := scanBot(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		bots = append(bots, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range bots {
		if bots[i].Targets, err = s.targets(bots[i].ID); err != nil {
			return nil, err
		}
	}
	return bots, nil
}

func (s *Store) GetBot(id int64) (Bot, error) {
	b, err := scanBot(s.db.QueryRow(`SELECT id, name, kind, enabled, device_id, config, current_state FROM bots WHERE id = ?`, id))
	if err != nil {
		return Bot{}, notFound(err)
	}
	b.Targets, err = s.targets(id)
	return b, err
}

func scanBot(row interface{ Scan(...any) error }) (Bot, error) {
	var b Bot
	var deviceID sql.NullInt64
	var config string
	err := row.Scan(&b.ID, &b.Name, &b.Kind, &b.Enabled, &deviceID, &config, &b.CurrentState)
	if deviceID.Valid {
		b.DeviceID = &deviceID.Int64
	}
	b.Config = json.RawMessage(config)
	return b, err
}

func (s *Store) targets(botID int64) ([]Target, error) {
	rows, err := s.db.Query(`SELECT kind, value, label FROM bot_targets WHERE bot_id = ? ORDER BY kind, label, value`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := []Target{}
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.Kind, &t.Value, &t.Label); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// SaveBot creates the bot (ID 0) or replaces it, targets included. Saving
// resets the bot's conversations, since the flow may have changed under them.
func (s *Store) SaveBot(b Bot) (Bot, error) {
	if err := b.validate(); err != nil {
		return Bot{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Bot{}, err
	}
	defer tx.Rollback()

	if b.DeviceID != nil {
		ok, err := deviceExists(tx, *b.DeviceID)
		if err != nil {
			return Bot{}, err
		}
		if !ok {
			return Bot{}, invalid("dispositivo não encontrado")
		}
	}

	if b.ID == 0 {
		res, err := tx.Exec(`INSERT INTO bots (name, kind, enabled, device_id, config, current_state) VALUES (?, ?, ?, ?, ?, ?)`,
			b.Name, b.Kind, b.Enabled, b.DeviceID, string(b.Config), b.CurrentState)
		if err != nil {
			return Bot{}, err
		}
		b.ID, _ = res.LastInsertId()
	} else {
		res, err := tx.Exec(`UPDATE bots SET name = ?, kind = ?, enabled = ?, device_id = ?, config = ?, current_state = ? WHERE id = ?`,
			b.Name, b.Kind, b.Enabled, b.DeviceID, string(b.Config), b.CurrentState, b.ID)
		if err != nil {
			return Bot{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return Bot{}, ErrNotFound
		}
		for _, q := range []string{`DELETE FROM bot_targets WHERE bot_id = ?`, `DELETE FROM conversations WHERE bot_id = ?`} {
			if _, err := tx.Exec(q, b.ID); err != nil {
				return Bot{}, err
			}
		}
	}

	for _, t := range b.Targets {
		_, err := tx.Exec(`INSERT INTO bot_targets (bot_id, kind, value, label) VALUES (?, ?, ?, ?)`,
			b.ID, t.Kind, t.Value, strings.TrimSpace(t.Label))
		if isUnique(err) {
			return Bot{}, ErrConflict
		}
		if err != nil {
			return Bot{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Bot{}, err
	}
	return s.GetBot(b.ID)
}

func (s *Store) DeleteBot(id int64) error {
	res, err := s.db.Exec(`DELETE FROM bots WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetBotState(id int64, state string) error {
	res, err := s.db.Exec(`UPDATE bots SET current_state = ? WHERE id = ?`, state, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Conversation returns the stored conversation of a chat, or nil if none.
func (s *Store) Conversation(botID int64, chat string) ([]byte, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM conversations WHERE bot_id = ? AND chat = ?`, botID, chat).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return []byte(data), err
}

// SaveConversation stores the conversation of a chat; nil data deletes it.
func (s *Store) SaveConversation(botID int64, chat string, data []byte) error {
	if data == nil {
		_, err := s.db.Exec(`DELETE FROM conversations WHERE bot_id = ? AND chat = ?`, botID, chat)
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO conversations (bot_id, chat, data) VALUES (?, ?, ?)
		ON CONFLICT (bot_id, chat) DO UPDATE SET data = excluded.data`,
		botID, chat, string(data))
	return err
}

// Conversations returns every stored conversation of a bot, keyed by chat.
func (s *Store) Conversations(botID int64) (map[string][]byte, error) {
	rows, err := s.db.Query(`SELECT chat, data FROM conversations WHERE bot_id = ?`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	convs := make(map[string][]byte)
	for rows.Next() {
		var chat, data string
		if err := rows.Scan(&chat, &data); err != nil {
			return nil, err
		}
		convs[chat] = []byte(data)
	}
	return convs, rows.Err()
}
