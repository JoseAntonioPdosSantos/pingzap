// Package web serves the management interface (a React app embedded in the
// binary) and its JSON API, plus the endpoint the ESP32 uses to fetch its
// MQTT topics.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"

	"pingzap/internal/bot"
	"pingzap/internal/store"
	"pingzap/internal/whatsapp"
)

//go:embed all:dist
var dist embed.FS

const sessionCookie = "pingzap_session"

type WhatsApp interface {
	Status() whatsapp.Status
	Pair() error
	Logout(ctx context.Context) error
	Contacts(ctx context.Context) ([]whatsapp.Contact, error)
	Groups(ctx context.Context) ([]whatsapp.Group, error)
}

type Server struct {
	store *store.Store
	bots  *bot.Service
	wa    WhatsApp
	mux   *http.ServeMux
}

func New(st *store.Store, bots *bot.Service, wa WhatsApp) http.Handler {
	s := &Server{store: st, bots: bots, wa: wa, mux: http.NewServeMux()}

	s.mux.HandleFunc("GET /api/auth/status", s.authStatus)
	s.mux.HandleFunc("POST /api/auth/setup", s.setup)
	s.mux.HandleFunc("POST /api/auth/login", s.login)
	s.mux.HandleFunc("POST /api/auth/logout", s.logout)

	s.handle("GET /api/users", s.listUsers)
	s.handle("POST /api/users", s.createUser)
	s.handle("PUT /api/users/{id}/password", s.setPassword)
	s.handle("DELETE /api/users/{id}", s.deleteUser)

	s.handle("GET /api/whatsapp", s.whatsappStatus)
	s.handle("POST /api/whatsapp/pair", s.whatsappPair)
	s.handle("POST /api/whatsapp/logout", s.whatsappLogout)
	s.handle("GET /api/whatsapp/contacts", s.whatsappContacts)
	s.handle("GET /api/whatsapp/groups", s.whatsappGroups)

	s.handle("GET /api/devices", s.listDevices)
	s.handle("POST /api/devices", s.createDevice)
	s.handle("PUT /api/devices/{id}", s.updateDevice)
	s.handle("DELETE /api/devices/{id}", s.deleteDevice)
	s.handle("POST /api/devices/{id}/token", s.regenerateToken)

	s.handle("GET /api/bots", s.listBots)
	s.handle("POST /api/bots", s.createBot)
	s.handle("GET /api/bots/{id}", s.getBot)
	s.handle("PUT /api/bots/{id}", s.updateBot)
	s.handle("DELETE /api/bots/{id}", s.deleteBot)
	s.handle("PUT /api/bots/{id}/state", s.setBotState)
	s.handle("GET /api/templates/gate", s.gateTemplate)

	s.mux.HandleFunc("GET /api/device/config", s.deviceConfig)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "rota não encontrada")
	})
	s.mux.Handle("/", spa())

	return s.csrf(s.mux)
}

// handle registers a route that requires a signed-in user.
func (s *Server) handle(pattern string, h func(http.ResponseWriter, *http.Request, store.User)) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.currentUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "faça login para continuar")
			return
		}
		h(w, r, user)
	})
}

// csrf rejects state-changing requests that are not JSON: a cross-site form
// cannot send application/json without the browser asking first (CORS).
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "envie JSON")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) currentUser(r *http.Request) (store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return store.User{}, false
	}
	user, err := s.store.SessionUser(c.Value)
	return user, err == nil
}

// spa serves the built React app, falling back to index.html so client-side
// routes work on reload.
func spa() http.Handler {
	files, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(files, path); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "interface web não compilada: rode o build do frontend (veja o README)", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeStoreError maps store errors to HTTP statuses. conflict is the
// message shown when a unique value is taken.
func writeStoreError(w http.ResponseWriter, err error, conflict string) {
	var ve *store.ValidationError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "não encontrado")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, conflict)
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, ve.Msg)
	default:
		log.Printf("web: %v", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return 0, false
	}
	return id, true
}

// reload refreshes the bot routes after a change, logging failures: the
// change itself is already saved.
func (s *Server) reload() {
	if err := s.bots.Reload(); err != nil {
		log.Printf("reload bots: %v", err)
	}
}
