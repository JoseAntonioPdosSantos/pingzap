package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"rsc.io/qr"

	"pingzap/internal/bot"
	"pingzap/internal/flow"
	"pingzap/internal/phone"
	"pingzap/internal/store"
)

// WhatsApp

func (s *Server) whatsappStatus(w http.ResponseWriter, r *http.Request, _ store.User) {
	status := s.wa.Status()
	resp := map[string]any{"state": status.State, "phone": status.Phone}
	if status.QR != "" {
		code, err := qr.Encode(status.QR, qr.L)
		if err == nil {
			code.Scale = 6
			resp["qr_image"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) whatsappPair(w http.ResponseWriter, r *http.Request, _ store.User) {
	if err := s.wa.Pair(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) whatsappLogout(w http.ResponseWriter, r *http.Request, _ store.User) {
	if err := s.wa.Logout(r.Context()); err != nil {
		writeStoreError(w, err, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) whatsappContacts(w http.ResponseWriter, r *http.Request, _ store.User) {
	contacts, err := s.wa.Contacts(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, contacts)
}

func (s *Server) whatsappGroups(w http.ResponseWriter, r *http.Request, _ store.User) {
	groups, err := s.wa.Groups(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// Devices

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request, _ store.User) {
	devices, err := s.store.ListDevices()
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request, _ store.User) {
	var d store.Device
	if !decode(w, r, &d) {
		return
	}
	d, err := s.store.CreateDevice(d)
	if err != nil {
		writeStoreError(w, err, "já existe um dispositivo com esse client ID")
		return
	}
	s.reload()
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var d store.Device
	if !decode(w, r, &d) {
		return
	}
	d.ID = id
	d, err := s.store.UpdateDevice(d)
	if err != nil {
		writeStoreError(w, err, "já existe um dispositivo com esse client ID")
		return
	}
	s.reload()
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteDevice(id); err != nil {
		writeStoreError(w, err, "")
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) regenerateToken(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := s.store.RegenerateDeviceToken(id)
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// deviceConfig is called by the ESP32 with its token to learn its MQTT
// client ID and topics.
func (s *Server) deviceConfig(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	d, err := s.store.DeviceByToken(strings.TrimSpace(token))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "token inválido")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"client_id":    d.ClientID,
		"cmd_topic":    d.CmdTopic,
		"data_topic":   d.DataTopic,
		"status_topic": d.StatusTopic,
	})
}

// Bots

func (s *Server) listBots(w http.ResponseWriter, r *http.Request, _ store.User) {
	bots, err := s.store.ListBots()
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, bots)
}

func (s *Server) getBot(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, err := s.store.GetBot(id)
	if err != nil {
		writeStoreError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) createBot(w http.ResponseWriter, r *http.Request, _ store.User) {
	s.saveBot(w, r, 0)
}

func (s *Server) updateBot(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.saveBot(w, r, id)
}

func (s *Server) saveBot(w http.ResponseWriter, r *http.Request, id int64) {
	var b store.Bot
	if !decode(w, r, &b) {
		return
	}
	b.ID = id
	if err := prepareBot(&b); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.SaveBot(b)
	if err != nil {
		writeStoreError(w, err, "um dos contatos ou grupos já está ligado a outro bot")
		return
	}
	s.reload()
	status := http.StatusOK
	if id == 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, saved)
}

// prepareBot validates the kind-specific configuration and normalizes the
// targets so they match incoming messages.
func prepareBot(b *store.Bot) error {
	switch b.Kind {
	case store.KindFlow:
		var f flow.Flow
		if err := json.Unmarshal(b.Config, &f); err != nil {
			return errors.New("configuração do fluxo inválida")
		}
		if err := f.Validate(); err != nil {
			return err
		}
		b.CurrentState = f.State(b.CurrentState).ID
	case store.KindAlert:
		var a bot.AlertConfig
		if err := json.Unmarshal(b.Config, &a); err != nil {
			return errors.New("configuração do alerta inválida")
		}
		if err := a.Validate(); err != nil {
			return err
		}
		b.CurrentState = ""
	default:
		return errors.New("tipo de bot inválido")
	}

	for i, t := range b.Targets {
		switch t.Kind {
		case store.TargetContact:
			v := phone.Normalize(t.Value)
			if len(v) < 12 {
				return errors.New("número inválido: " + t.Value + " (use DDD + telefone)")
			}
			b.Targets[i].Value = v
		case store.TargetGroup:
			if !strings.HasSuffix(t.Value, "@g.us") {
				return errors.New("grupo inválido: " + t.Value)
			}
		}
	}
	return nil
}

func (s *Server) deleteBot(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteBot(id); err != nil {
		writeStoreError(w, err, "")
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setBotState(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.bots.SetState(id, body.State); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeStoreError(w, err, "")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) gateTemplate(w http.ResponseWriter, r *http.Request, _ store.User) {
	writeJSON(w, http.StatusOK, flow.GateTemplate())
}
