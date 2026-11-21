package web

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"pingzap/internal/bot"
	"pingzap/internal/store"
)

// The web remote has no login: the token in the link is the credential, so
// these routes answer anyone who has it and say nothing to who does not.

func (s *Server) remoteView(w http.ResponseWriter, r *http.Request) {
	view, err := s.bots.Remote(r.PathValue("token"))
	if err != nil {
		writeRemoteError(w, err, nil)
		return
	}
	writeRemote(w, http.StatusOK, view)
}

func (s *Server) remoteAct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State  string `json:"state"`
		Option int    `json:"option"`
		Retry  bool   `json:"retry"`
	}
	if !decode(w, r, &body) {
		return
	}
	view, err := s.bots.RemoteAct(r.PathValue("token"), body.State, body.Option, body.Retry)
	if err != nil {
		writeRemoteError(w, err, &view)
		return
	}
	writeRemote(w, http.StatusOK, view)
}

func writeRemote(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, v)
}

// writeRemoteError answers a failed web remote request. view, when the
// error leaves one, tells the page what the remote looks like now.
func writeRemoteError(w http.ResponseWriter, err error, view *bot.Remote) {
	status := http.StatusInternalServerError
	msg := "erro interno"
	var locked *bot.RemoteLocked
	switch {
	case errors.As(err, &locked):
		// 410: this access is over. 423: it is only closed for now, so the
		// page keeps asking until it opens.
		status, msg, view = http.StatusLocked, locked.Reason, nil
		if locked.Permanent {
			status = http.StatusGone
		}
	case errors.Is(err, bot.ErrRemoteGone):
		status, msg, view = http.StatusNotFound, err.Error(), nil
	case errors.Is(err, bot.ErrRemoteUnverified):
		status, msg, view = http.StatusServiceUnavailable, err.Error(), nil
	case errors.Is(err, bot.ErrRemoteStale):
		status, msg = http.StatusConflict, err.Error()
	case errors.Is(err, bot.ErrRemoteDevice):
		status, msg, view = http.StatusBadGateway, err.Error(), nil
	default:
		log.Printf("web remote: %v", err)
		view = nil
	}
	body := map[string]any{"error": msg}
	if view != nil {
		body["remote"] = view
	}
	writeRemote(w, status, body)
}

// remotePage serves the app for a web remote link. The address holds the
// token, so it must not be cached, indexed or sent along as a referrer.
func remotePage(app http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		app.ServeHTTP(w, r)
	})
}

// botRemote lists who has a web remote of the bot and its latest uses.
func (s *Server) botRemote(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	links, err := s.store.RemoteLinks(id)
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	uses, err := s.store.RemoteUses(id, 50)
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	passes, err := s.store.Passes(id)
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": links, "uses": uses, "passes": s.passViews(passes)})
}

func (s *Server) revokeRemote(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	link, err := strconv.ParseInt(r.PathValue("link"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	if err := s.store.DeleteRemoteLink(id, link); err != nil {
		writeStoreError(w, err, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
