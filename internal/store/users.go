package store

import (
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionTTL = 7 * 24 * time.Hour

var (
	ErrInvalidLogin = errors.New("usuário ou senha inválidos")
	ErrLastUser     = invalid("não é possível remover o último usuário")
	ErrWeakPassword = invalid("a senha precisa ter ao menos 8 caracteres")
)

// dummyHash is compared against when the user does not exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("pingzap"), bcrypt.DefaultCost)

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, username, created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var u User
		var created int64
		if err := rows.Scan(&u.ID, &u.Username, &created); err != nil {
			return nil, err
		}
		u.CreatedAt = time.Unix(created, 0)
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return User{}, invalid("informe o nome de usuário")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		username, hash, now.Unix())
	if isUnique(err) {
		return User{}, ErrConflict
	}
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, CreatedAt: now}, nil
}

func (s *Store) SetPassword(id int64, password string) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// Changing the password signs the user out everywhere.
	_, err = s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id)
	return err
}

func (s *Store) DeleteUser(id int64) error {
	n, err := s.CountUsers()
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastUser
	}
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Login checks the credentials and opens a session, returning its token.
func (s *Store) Login(username, password string) (string, User, error) {
	var u User
	var hash string
	var created int64
	err := s.db.QueryRow(`SELECT id, username, password_hash, created_at FROM users WHERE username = ?`,
		strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &hash, &created)
	if err != nil {
		// Compare anyway so a missing user takes as long as a wrong password.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", User{}, ErrInvalidLogin
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", User{}, ErrInvalidLogin
	}
	u.CreatedAt = time.Unix(created, 0)

	now := time.Now()
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Unix()); err != nil {
		return "", User{}, err
	}
	token := randomToken()
	if _, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(token), u.ID, now.Add(sessionTTL).Unix()); err != nil {
		return "", User{}, err
	}
	return token, u, nil
}

// SessionUser returns the user of a valid session token.
func (s *Store) SessionUser(token string) (User, error) {
	var u User
	var created int64
	err := s.db.QueryRow(`
		SELECT u.id, u.username, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at >= ?`,
		hashToken(token), time.Now().Unix()).Scan(&u.ID, &u.Username, &created)
	if err != nil {
		return User{}, notFound(err)
	}
	u.CreatedAt = time.Unix(created, 0)
	return u, nil
}

func (s *Store) Logout(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

func hashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}
