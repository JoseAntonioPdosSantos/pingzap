package web

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"pingzap/internal/bot"
	"pingzap/internal/store"
)

// Temporary accesses ("passes") are web remotes made here, in the management
// interface, for people who are not in the bot's chats. The link is built
// from the public address, so it can be copied and sent to whoever needs it.

// passView is a pass as the interface shows it.
type passView struct {
	store.RemotePass
	URL string `json:"url"`
	// Path is the link without the public address, for when none is set.
	Path       string `json:"path"`
	UsesLeft   int    `json:"uses_left"` // -1 = no limit
	Status     string `json:"status"`
	StatusText string `json:"status_text"`
}

func (s *Server) passViews(passes []store.RemotePass) []passView {
	now := time.Now()
	base := s.bots.PublicURL()
	views := make([]passView, 0, len(passes))
	for _, p := range passes {
		status, text := p.Status(now)
		v := passView{RemotePass: p, Path: "/c/" + p.Token, UsesLeft: p.UsesLeft(), Status: string(status), StatusText: text}
		if base != "" {
			v.URL = base + v.Path
		}
		views = append(views, v)
	}
	return views
}

func passIDs(w http.ResponseWriter, r *http.Request) (bot, pass int64, ok bool) {
	bot, ok = pathID(w, r)
	if !ok {
		return
	}
	pass, err := strconv.ParseInt(r.PathValue("pass"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return 0, 0, false
	}
	return bot, pass, true
}

func (s *Server) createPass(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.PassInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.store.CreatePass(id, in)
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, s.passViews([]store.RemotePass{p})[0])
}

func (s *Server) updatePass(w http.ResponseWriter, r *http.Request, _ store.User) {
	bot, pass, ok := passIDs(w, r)
	if !ok {
		return
	}
	var in store.PassInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.store.UpdatePass(bot, pass, in)
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, s.passViews([]store.RemotePass{p})[0])
}

func (s *Server) deletePass(w http.ResponseWriter, r *http.Request, _ store.User) {
	bot, pass, ok := passIDs(w, r)
	if !ok {
		return
	}
	if err := s.store.DeletePass(bot, pass); err != nil {
		writeStoreError(w, err, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sendPass sends the link of a pass to a phone number on WhatsApp.
func (s *Server) sendPass(w http.ResponseWriter, r *http.Request, _ store.User) {
	botID, passID, ok := passIDs(w, r)
	if !ok {
		return
	}
	var body struct {
		Phone string `json:"phone"`
	}
	if !decode(w, r, &body) {
		return
	}
	pass, err := s.store.Pass(botID, passID)
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	if status, text := pass.Status(time.Now()); status.Permanent() {
		writeError(w, http.StatusConflict, text+" Renove o acesso antes de enviar.")
		return
	}
	err = s.bots.SendPassLink(pass, body.Phone)
	var ve *store.ValidationError
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, ve.Msg)
	case errors.Is(err, bot.ErrNoPublicURL):
		writeError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("web: send pass %d: %v", passID, err)
		writeError(w, http.StatusBadGateway, err.Error())
	}
}
