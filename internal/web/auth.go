package web

import (
	"errors"
	"net/http"

	"pingzap/internal/store"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers()
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	resp := map[string]any{"setup_required": n == 0}
	if user, ok := s.currentUser(r); ok {
		resp["user"] = user
	}
	writeJSON(w, http.StatusOK, resp)
}

// setup creates the first user. It only works while there is none.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	n, err := s.store.CountUsers()
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	if n > 0 {
		writeError(w, http.StatusForbidden, "o sistema já foi configurado")
		return
	}
	if _, err := s.store.CreateUser(c.Username, c.Password); err != nil {
		writeStoreError(w, err, "usuário já existe")
		return
	}
	s.startSession(w, c)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	s.startSession(w, c)
}

func (s *Server) startSession(w http.ResponseWriter, c credentials) {
	token, user, err := s.store.Login(c.Username, c.Password)
	if errors.Is(err, store.ErrInvalidLogin) {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.store.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ store.User) {
	users, err := s.store.ListUsers()
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, _ store.User) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	user, err := s.store.CreateUser(c.Username, c.Password)
	if err != nil {
		writeStoreError(w, err, "usuário já existe")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	if err := s.store.SetPassword(id, c.Password); err != nil {
		writeStoreError(w, err, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, current store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == current.ID {
		writeError(w, http.StatusBadRequest, "você não pode remover o próprio usuário")
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		writeStoreError(w, err, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
