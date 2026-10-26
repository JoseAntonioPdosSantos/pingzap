package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"

	"pingzap/internal/store"
	"pingzap/internal/whatsapp"
)

const (
	// silenceLimit is how long a device may go without publishing before it
	// counts as down. It sends telemetry every 5 s, and a reconnect takes a
	// few seconds, so only a real outage lasts this long.
	silenceLimit = time.Minute
	// outageAskEvery is how often the outage question is repeated to a chat
	// that has not answered it.
	outageAskEvery = 3 * time.Minute
)

// outage is a device that stopped answering. Every chat of the bots using it
// is told and asked to answer until it does; all of them hear when the
// device is back.
type outage struct {
	device store.Device
	since  time.Time // last time the device was heard
	chats  []*outageChat
}

type outageChat struct {
	keys     []string // chat keys of the target (phone number variants)
	jid      types.JID
	asked    time.Time
	answered bool
}

func (c *outageChat) matches(key string) bool {
	for _, k := range c.keys {
		if k == key {
			return true
		}
	}
	return false
}

// heard records that a device published something. The LWT "offline" does
// not count: it is the broker saying the device is gone.
func (s *Service) heard(d store.Device, status bool, payload string, now time.Time) {
	if status && strings.EqualFold(strings.TrimSpace(payload), "offline") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHeard[d.ID] = now
	o, ok := s.outages[d.ID]
	if !ok {
		return
	}
	delete(s.outages, d.ID)
	log.Printf("device %q is back after %s", d.Name, now.Sub(o.since).Round(time.Second))
	text := fmt.Sprintf("✅ *Sistema de volta*\nO dispositivo *%s* voltou a responder depois de %s fora do ar. "+
		"Os comandos já funcionam de novo.", d.Name, duration(now.Sub(o.since)))
	for _, c := range o.chats {
		s.send(c.jid, text)
	}
}

// watch opens an outage for every device silent for longer than
// silenceLimit and repeats the question to the chats that have not answered.
func (s *Service) watch(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	used := make(map[int64]store.Device)
	for _, r := range s.bots {
		if r.device != nil {
			used[r.device.ID] = *r.device
		}
	}
	for id := range s.outages {
		if _, ok := used[id]; !ok {
			delete(s.outages, id)
		}
	}
	for id, d := range used {
		last, ok := s.lastHeard[id]
		if !ok {
			// Start counting when the watch begins (pingzap just started or
			// the device was just assigned to a bot).
			s.lastHeard[id] = now
			continue
		}
		if o, ok := s.outages[id]; ok {
			for _, c := range o.chats {
				if !c.answered && now.Sub(c.asked) >= outageAskEvery {
					s.askOutage(o, c, now, "")
				}
			}
			continue
		}
		if now.Sub(last) <= silenceLimit {
			continue
		}
		o := &outage{device: d, since: last, chats: s.outageChats(id)}
		s.outages[id] = o
		log.Printf("device %q silent since %s; telling %d chat(s)", d.Name, last.Format(time.RFC3339), len(o.chats))
		for _, c := range o.chats {
			s.askOutage(o, c, now, "")
		}
	}
}

// outageChats lists the chats of the enabled bots that use the device.
func (s *Service) outageChats(deviceID int64) []*outageChat {
	var chats []*outageChat
	seen := make(map[string]bool)
	for _, r := range s.bots {
		if r.device == nil || r.device.ID != deviceID {
			continue
		}
		for _, t := range r.bot.Targets {
			keys := targetKeys(t)
			if len(keys) == 0 || seen[keys[0]] {
				continue
			}
			seen[keys[0]] = true
			jid, err := s.targetJID(t)
			if err != nil {
				log.Printf("bot %q: can not warn %s: %v", r.bot.Name, t.Value, err)
				continue
			}
			chats = append(chats, &outageChat{keys: keys, jid: jid})
		}
	}
	return chats
}

func (s *Service) targetJID(t store.Target) (types.JID, error) {
	if t.Kind == store.TargetGroup {
		return types.ParseJID(t.Value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	return s.wa.ContactJID(ctx, t.Value)
}

func (s *Service) askOutage(o *outage, c *outageChat, now time.Time, prefix string) {
	c.asked = now
	s.send(c.jid, prefix+fmt.Sprintf("⚠️ *Sistema fora do ar*\nO dispositivo *%s* não responde há %s. "+
		"Enquanto isso, os comandos (como abrir o portão) não chegam até ele.\n\n"+
		"*1* - Vou analisar o caso\n*2* - Ciente", o.device.Name, duration(now.Sub(o.since))))
}

// answerOutage takes the answer to an open outage question in the chat. It
// reports whether the message was used, so the bot does not also take it.
func (s *Service) answerOutage(r *route, m whatsapp.Message, now time.Time) bool {
	if r.device == nil {
		return false
	}
	o, ok := s.outages[r.device.ID]
	if !ok {
		return false
	}
	var c *outageChat
	for _, oc := range o.chats {
		if oc.matches(chatKey(m)) {
			c = oc
		}
	}
	// The bot writes from my account: only my input-looking messages count.
	if c == nil || c.answered || !isInput(m.Text) {
		return false
	}
	switch strings.TrimSpace(m.Text) {
	case "1":
		c.answered = true
		s.send(m.Chat, fmt.Sprintf("Combinado: o caso do *%s* está em análise. Aviso aqui quando ele voltar.", o.device.Name))
	case "2":
		c.answered = true
		s.send(m.Chat, fmt.Sprintf("Ciência registrada. Aviso aqui quando o *%s* voltar.", o.device.Name))
	default:
		s.askOutage(o, c, now, "Não entendi a resposta.\n\n")
	}
	return true
}

// duration writes d as "5 min" or "2 h 05 min".
func duration(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m < 1 {
		m = 1
	}
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %02d min", m/60, m%60)
}
