package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RemotePass is a temporary web remote made in the management interface for
// someone who has no WhatsApp relation with the bot (a visitor, a courier, a
// cleaner). Whoever opens its link acts under the pass's name, while the
// pass is valid: inside its dates, with uses left, and inside its schedule.
type RemotePass struct {
	ID    int64  `json:"id"`
	BotID int64  `json:"bot_id"`
	Name  string `json:"name"`
	// Token is the secret in the link.
	Token     string     `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
	StartsAt  *time.Time `json:"starts_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	// MaxUses is how many commands the pass may send; 0 means no limit.
	// Sending a command again after it failed does not count.
	MaxUses int `json:"max_uses"`
	Uses    int `json:"uses"`
	// Timezone is the IANA zone the schedule is read in.
	Timezone string   `json:"timezone"`
	Schedule Schedule `json:"schedule"`
}

// PassInput is what the interface edits of a pass.
type PassInput struct {
	Name      string     `json:"name"`
	StartsAt  *time.Time `json:"starts_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	MaxUses   int        `json:"max_uses"`
	Timezone  string     `json:"timezone"`
	Schedule  Schedule   `json:"schedule"`
	// ResetUses starts counting the uses again (to renew a pass).
	ResetUses bool `json:"reset_uses"`
}

// PassStatus is where a pass stands at a moment.
type PassStatus string

const (
	PassActive    PassStatus = "active"
	PassScheduled PassStatus = "scheduled" // its start date has not come
	PassOutside   PassStatus = "outside"   // outside the days and hours allowed
	PassExpired   PassStatus = "expired"
	PassUsedUp    PassStatus = "used_up"
)

// Permanent reports whether the status can not change by itself with time
// (an expired or used up pass never works again, unless it is renewed).
func (s PassStatus) Permanent() bool { return s == PassExpired || s == PassUsedUp }

const maxPassUses = 1000

// Location is the pass's time zone.
func (p RemotePass) Location() *time.Location {
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Status says whether the pass works at now, and if not, why, in words for
// the person holding the link.
func (p RemotePass) Status(now time.Time) (PassStatus, string) {
	loc := p.Location()
	at := func(t time.Time) string { return t.In(loc).Format("02/01 15:04") }
	switch {
	case p.ExpiresAt != nil && !now.Before(*p.ExpiresAt):
		return PassExpired, "Este acesso expirou em " + at(*p.ExpiresAt) + "."
	case p.MaxUses > 0 && p.Uses >= p.MaxUses:
		return PassUsedUp, "Este acesso já foi usado o número de vezes permitido."
	case p.StartsAt != nil && now.Before(*p.StartsAt):
		return PassScheduled, "Este acesso só começa a valer em " + at(*p.StartsAt) + "."
	case !p.Schedule.Allows(now.In(loc)):
		msg := "Fora do horário permitido (" + p.Schedule.Describe() + ")."
		if next, ok := p.Schedule.Next(now.In(loc)); ok && (p.ExpiresAt == nil || next.Before(*p.ExpiresAt)) {
			msg += " Volta a funcionar em " + next.Format("02/01 15:04") + "."
		}
		return PassOutside, msg
	}
	return PassActive, ""
}

// UsesLeft is how many commands remain, or -1 without a limit.
func (p RemotePass) UsesLeft() int {
	if p.MaxUses == 0 {
		return -1
	}
	return max(p.MaxUses-p.Uses, 0)
}

const passColumns = `id, bot_id, name, token, created_at, starts_at, expires_at, last_used, max_uses, uses, timezone, schedule`

func scanPass(row interface{ Scan(...any) error }) (RemotePass, error) {
	var p RemotePass
	var created, starts, expires, lastUsed int64
	var schedule string
	err := row.Scan(&p.ID, &p.BotID, &p.Name, &p.Token, &created, &starts, &expires, &lastUsed, &p.MaxUses, &p.Uses, &p.Timezone, &schedule)
	if err != nil {
		return p, err
	}
	p.CreatedAt = time.Unix(created, 0)
	for _, f := range []struct {
		unix int64
		to   **time.Time
	}{{starts, &p.StartsAt}, {expires, &p.ExpiresAt}, {lastUsed, &p.LastUsed}} {
		if f.unix > 0 {
			t := time.Unix(f.unix, 0)
			*f.to = &t
		}
	}
	if err := json.Unmarshal([]byte(schedule), &p.Schedule); err != nil {
		return p, fmt.Errorf("schedule of pass %d: %w", p.ID, err)
	}
	if p.Schedule.Windows == nil {
		p.Schedule.Windows = []Window{}
	}
	return p, nil
}

func unixOrZero(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.Unix()
}

// clean validates the input and returns it ready to save.
func (in PassInput) clean() (PassInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		return in, invalid("dê um nome ao acesso (quem vai usar)")
	case len([]rune(in.Name)) > 60:
		return in, invalid("o nome pode ter até 60 caracteres")
	case in.MaxUses < 0 || in.MaxUses > maxPassUses:
		return in, invalid(fmt.Sprintf("o limite de usos vai de 0 (sem limite) a %d", maxPassUses))
	}
	if in.StartsAt != nil && in.StartsAt.IsZero() {
		in.StartsAt = nil
	}
	if in.ExpiresAt != nil && in.ExpiresAt.IsZero() {
		in.ExpiresAt = nil
	}
	if in.StartsAt != nil && in.ExpiresAt != nil && !in.ExpiresAt.After(*in.StartsAt) {
		return in, invalid("a expiração deve ser depois do início")
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return in, invalid("fuso horário inválido")
	}
	if err := in.Schedule.Validate(); err != nil {
		return in, err
	}
	if in.Schedule.Windows == nil {
		in.Schedule.Windows = []Window{}
	}
	return in, nil
}

// CreatePass makes a pass for the flow bot and returns it, with its token.
func (s *Store) CreatePass(botID int64, in PassInput) (RemotePass, error) {
	b, err := s.GetBot(botID)
	if err != nil {
		return RemotePass{}, err
	}
	if b.Kind != KindFlow {
		return RemotePass{}, invalid("só bots de fluxo têm controle remoto")
	}
	in, err = in.clean()
	if err != nil {
		return RemotePass{}, err
	}
	schedule, _ := json.Marshal(in.Schedule)
	res, err := s.db.Exec(`
		INSERT INTO remote_passes (bot_id, name, token, created_at, starts_at, expires_at, max_uses, timezone, schedule)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		botID, in.Name, randomToken(), time.Now().Unix(), unixOrZero(in.StartsAt), unixOrZero(in.ExpiresAt),
		in.MaxUses, in.Timezone, string(schedule))
	if err != nil {
		return RemotePass{}, err
	}
	id, _ := res.LastInsertId()
	return s.pass(botID, id)
}

// Pass returns one of the bot's passes.
func (s *Store) Pass(botID, id int64) (RemotePass, error) { return s.pass(botID, id) }

func (s *Store) pass(botID, id int64) (RemotePass, error) {
	p, err := scanPass(s.db.QueryRow(`SELECT `+passColumns+` FROM remote_passes WHERE id = ? AND bot_id = ?`, id, botID))
	return p, notFound(err)
}

// UpdatePass changes a pass. The link stays the same.
func (s *Store) UpdatePass(botID, id int64, in PassInput) (RemotePass, error) {
	in, err := in.clean()
	if err != nil {
		return RemotePass{}, err
	}
	schedule, _ := json.Marshal(in.Schedule)
	res, err := s.db.Exec(`
		UPDATE remote_passes SET name = ?, starts_at = ?, expires_at = ?, max_uses = ?, timezone = ?, schedule = ?,
			uses = CASE WHEN ? THEN 0 ELSE uses END
		WHERE id = ? AND bot_id = ?`,
		in.Name, unixOrZero(in.StartsAt), unixOrZero(in.ExpiresAt), in.MaxUses, in.Timezone, string(schedule),
		in.ResetUses, id, botID)
	if err != nil {
		return RemotePass{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return RemotePass{}, ErrNotFound
	}
	return s.pass(botID, id)
}

// Passes lists the bot's passes, newest first.
func (s *Store) Passes(botID int64) ([]RemotePass, error) {
	rows, err := s.db.Query(`SELECT `+passColumns+` FROM remote_passes WHERE bot_id = ? ORDER BY created_at DESC, id DESC`, botID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	passes := []RemotePass{}
	for rows.Next() {
		p, err := scanPass(rows)
		if err != nil {
			return nil, err
		}
		passes = append(passes, p)
	}
	return passes, rows.Err()
}

func (s *Store) PassByToken(token string) (RemotePass, error) {
	if token == "" {
		return RemotePass{}, ErrNotFound
	}
	p, err := scanPass(s.db.QueryRow(`SELECT `+passColumns+` FROM remote_passes WHERE token = ?`, token))
	return p, notFound(err)
}

// DeletePass revokes a pass: its link stops working for good.
func (s *Store) DeletePass(botID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM remote_passes WHERE id = ? AND bot_id = ?`, id, botID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimPassUse takes one of the pass's uses, before the command is sent, so
// two presses at once can not go past the limit. It returns false when none
// is left.
func (s *Store) ClaimPassUse(id int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE remote_passes SET uses = uses + 1 WHERE id = ? AND (max_uses = 0 OR uses < max_uses)`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReleasePassUse gives back a use claimed for a command that could not be sent.
func (s *Store) ReleasePassUse(id int64) error {
	_, err := s.db.Exec(`UPDATE remote_passes SET uses = MAX(uses - 1, 0) WHERE id = ?`, id)
	return err
}

// LogPassUse records an action taken through a pass. The history keeps the
// pass's name, so it outlives the pass.
func (s *Store) LogPassUse(p RemotePass, u RemoteUse) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := u.At.Unix()
	if _, err := tx.Exec(`
		INSERT INTO remote_uses (bot_id, phone, name, action, command, state, at) VALUES (?, '', ?, ?, ?, ?, ?)`,
		p.BotID, p.Name, u.Action, u.Command, u.State, at); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE remote_passes SET last_used = ? WHERE id = ?`, at, p.ID); err != nil {
		return err
	}
	return tx.Commit()
}
