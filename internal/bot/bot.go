// Package bot routes WhatsApp messages to the bot that answers each chat and
// runs it: flow bots hold a menu conversation and publish commands; alert
// bots publish ON when a message arrives and OFF once everyone was answered.
// It also records the telemetry and status the devices send back.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types"

	"pingzap/internal/alert"
	"pingzap/internal/flow"
	"pingzap/internal/phone"
	"pingzap/internal/store"
	"pingzap/internal/whatsapp"
)

const (
	reminderCheck = 20 * time.Second
	sendTimeout   = 30 * time.Second
)

// AlertConfig is the configuration of an alert bot.
type AlertConfig struct {
	// Keyword restricts the trigger to messages containing it. When empty,
	// any message triggers, including those without text.
	Keyword    string `json:"keyword"`
	OnPayload  string `json:"on_payload"`
	OffPayload string `json:"off_payload"`
}

func (a AlertConfig) Validate() error {
	if strings.TrimSpace(a.OnPayload) == "" || strings.TrimSpace(a.OffPayload) == "" {
		return errors.New("informe os comandos de ligar e desligar o alerta")
	}
	return nil
}

type MQTT interface {
	Publish(topic, payload string) error
	SetSubscriptions(topics []string)
}

type Sender interface {
	SendText(ctx context.Context, chat types.JID, text string) error
}

// Metrics stores telemetry for graphs (InfluxDB / Grafana).
type Metrics interface {
	Telemetry(device, clientID string, payload []byte, at time.Time)
	Status(device, clientID, status string, at time.Time)
}

type Service struct {
	store   *store.Store
	mqtt    MQTT
	wa      Sender
	metrics Metrics // optional

	// mu serializes message handling, reminders and reloads, so a bot's
	// state and conversations change one message at a time.
	mu sync.Mutex
	// routes maps a chat key (see chatKey) to the bot answering it.
	routes map[string]*route
	bots   map[int64]*route
	// topics maps the devices' data and status topics back to them.
	topics   map[string]deviceTopic
	trackers map[int64]*alert.Tracker
}

type route struct {
	bot    store.Bot
	device *store.Device
	flow   flow.Flow
	alert  AlertConfig
}

type deviceTopic struct {
	device store.Device
	status bool
}

// savedConversation is what the store keeps per chat: the flow progress and
// where to send reminders.
type savedConversation struct {
	Chat string `json:"chat"`
	flow.Conversation
}

// New creates the service; metrics may be nil.
func New(st *store.Store, mq MQTT, wa Sender, metrics Metrics) *Service {
	return &Service{store: st, mqtt: mq, wa: wa, metrics: metrics, trackers: make(map[int64]*alert.Tracker)}
}

// Reload rebuilds the routes and subscriptions from the database. Call it
// after any change to bots or devices.
func (s *Service) Reload() error {
	bots, err := s.store.ListBots()
	if err != nil {
		return err
	}
	devices, err := s.store.ListDevices()
	if err != nil {
		return err
	}

	byID := make(map[int64]*store.Device)
	topics := make(map[string]deviceTopic)
	var subscriptions []string
	for i := range devices {
		d := &devices[i]
		byID[d.ID] = d
		topics[d.DataTopic] = deviceTopic{device: *d}
		topics[d.StatusTopic] = deviceTopic{device: *d, status: true}
		subscriptions = append(subscriptions, d.DataTopic, d.StatusTopic)
	}

	routes := make(map[string]*route)
	botRoutes := make(map[int64]*route)
	for _, b := range bots {
		if !b.Enabled {
			continue
		}
		r := &route{bot: b}
		if b.DeviceID != nil {
			r.device = byID[*b.DeviceID]
		}
		var err error
		switch b.Kind {
		case store.KindFlow:
			err = json.Unmarshal(b.Config, &r.flow)
			if err == nil {
				err = r.flow.Validate()
			}
		case store.KindAlert:
			err = json.Unmarshal(b.Config, &r.alert)
		}
		if err != nil {
			log.Printf("bot %q skipped: invalid configuration: %v", b.Name, err)
			continue
		}
		botRoutes[b.ID] = r
		for _, t := range b.Targets {
			for _, key := range targetKeys(t) {
				routes[key] = r
			}
		}
	}

	s.mu.Lock()
	s.routes, s.bots, s.topics = routes, botRoutes, topics
	for id := range s.trackers {
		if r, ok := botRoutes[id]; !ok || r.bot.Kind != store.KindAlert {
			delete(s.trackers, id)
		}
	}
	s.mu.Unlock()

	s.mqtt.SetSubscriptions(subscriptions)
	return nil
}

// targetKeys returns the chat keys a target answers to: every WhatsApp form
// of a phone number, or the group JID.
func targetKeys(t store.Target) []string {
	if t.Kind == store.TargetGroup {
		return []string{"group:" + t.Value}
	}
	var keys []string
	for _, v := range phone.Variants(t.Value) {
		keys = append(keys, "contact:"+v)
	}
	return keys
}

func chatKey(m whatsapp.Message) string {
	if m.IsGroup() {
		return "group:" + m.Group
	}
	return "contact:" + m.Phone
}

// HandleMessage processes a WhatsApp message from any chat.
func (s *Service) HandleMessage(m whatsapp.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.routes[chatKey(m)]
	if !ok {
		return
	}
	log.Printf("bot %q (%s): message in %s (from me: %v)", r.bot.Name, r.bot.Kind, chatKey(m), m.FromMe)
	switch r.bot.Kind {
	case store.KindAlert:
		s.handleAlert(r, m)
	case store.KindFlow:
		// The bot writes from my own account, so my messages only count when
		// they look like input (a command or a number): the bot's own
		// replies never do, which keeps it from answering itself.
		if !m.FromMe || isInput(m.Text) {
			s.handleFlow(r, m)
		}
	}
}

// isInput reports whether the text is a command ("/start") or a number
// ("1"), the only things a person types in a flow.
func isInput(text string) bool {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "/") {
		return !strings.ContainsAny(text, " \n")
	}
	_, err := strconv.Atoi(text)
	return err == nil
}

func (s *Service) handleAlert(r *route, m whatsapp.Message) {
	tracker, ok := s.trackers[r.bot.ID]
	if !ok {
		tracker = alert.NewTracker()
		s.trackers[r.bot.ID] = tracker
	}
	chat := chatKey(m)

	if m.FromMe {
		// The LED only turns off once everyone who triggered it got a reply.
		if n := tracker.Ack(chat); n > 0 {
			log.Printf("bot %q: %d chat(s) still awaiting a reply; alert kept on", r.bot.Name, n)
			return
		}
		if err := s.publish(r, r.alert.OffPayload); err != nil {
			log.Printf("bot %q: %v", r.bot.Name, err)
		}
		return
	}
	if !matchesKeyword(m.Text, r.alert.Keyword) {
		return
	}
	log.Printf("bot %q: alert triggered by %s", r.bot.Name, chat)
	tracker.Raise(chat)
	if err := s.publish(r, r.alert.OnPayload); err != nil {
		log.Printf("bot %q: %v", r.bot.Name, err)
	}
}

func (s *Service) handleFlow(r *route, m whatsapp.Message) {
	key := chatKey(m)
	saved, err := s.conversation(r.bot.ID, key)
	if err != nil {
		log.Printf("bot %q: load conversation: %v", r.bot.Name, err)
		return
	}

	res, ok := r.flow.Handle(r.bot.CurrentState, saved.Conversation, m.Text, time.Now())
	if !ok {
		return
	}
	for _, cmd := range res.Publish {
		if err := s.publish(r, cmd); err != nil {
			log.Printf("bot %q: %v", r.bot.Name, err)
			// Keep the conversation where it was so the person can retry.
			s.send(m.Chat, "Não consegui enviar o comando ao dispositivo. Tente novamente em instantes.")
			return
		}
	}
	if res.NewState != "" && res.NewState != r.bot.CurrentState {
		if err := s.store.SetBotState(r.bot.ID, res.NewState); err != nil {
			log.Printf("bot %q: save state: %v", r.bot.Name, err)
			return
		}
		log.Printf("bot %q: state %s -> %s", r.bot.Name, r.bot.CurrentState, res.NewState)
		r.bot.CurrentState = res.NewState
	}
	s.saveConversation(r, key, m.Chat, res.Conversation)
	for _, reply := range res.Replies {
		s.send(m.Chat, reply)
	}
}

// RunReminders repeats unanswered confirmation questions until ctx ends.
func (s *Service) RunReminders(ctx context.Context) {
	ticker := time.NewTicker(reminderCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.remind(now)
		}
	}
}

func (s *Service) remind(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.bots {
		if r.bot.Kind != store.KindFlow || r.flow.ReminderMinutes <= 0 {
			continue
		}
		convs, err := s.store.Conversations(r.bot.ID)
		if err != nil {
			log.Printf("bot %q: load conversations: %v", r.bot.Name, err)
			continue
		}
		for key, data := range convs {
			var saved savedConversation
			if json.Unmarshal(data, &saved) != nil {
				continue
			}
			res, ok := r.flow.Remind(saved.Conversation, now)
			if !ok {
				continue
			}
			chat, err := types.ParseJID(saved.Chat)
			if err != nil {
				continue
			}
			for _, cmd := range res.Publish {
				if err := s.publish(r, cmd); err != nil {
					// Try the alarm again on the next reminder.
					log.Printf("bot %q: alarm: %v", r.bot.Name, err)
					res.Conversation.Alarm = false
					res.Replies = res.Replies[:1]
					break
				}
				log.Printf("bot %q: no answer in %s; alarm fired", r.bot.Name, key)
			}
			s.saveConversation(r, key, chat, res.Conversation)
			for _, reply := range res.Replies {
				s.send(chat, reply)
			}
		}
	}
}

// SetState changes a flow bot's state by hand, e.g. after the gate was
// moved without the bot.
func (s *Service) SetState(botID int64, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.store.GetBot(botID)
	if err != nil {
		return err
	}
	if b.Kind != store.KindFlow {
		return errors.New("só bots de fluxo têm estado")
	}
	var f flow.Flow
	if err := json.Unmarshal(b.Config, &f); err != nil {
		return err
	}
	if f.State(state).ID != state {
		return fmt.Errorf("estado %q não existe", state)
	}
	if err := s.store.SetBotState(botID, state); err != nil {
		return err
	}
	if r, ok := s.bots[botID]; ok {
		r.bot.CurrentState = state
	}
	return nil
}

// HandleTelemetry records a message published by a device.
func (s *Service) HandleTelemetry(topic string, payload []byte) {
	s.mu.Lock()
	t, ok := s.topics[topic]
	s.mu.Unlock()
	if !ok {
		return
	}
	log.Printf("[%s] %s", topic, payload)
	now := time.Now()
	d := t.device
	var err error
	if t.status {
		err = s.store.SetDeviceStatus(d.ID, string(payload))
		if s.metrics != nil {
			s.metrics.Status(d.Name, d.ClientID, string(payload), now)
		}
	} else {
		err = s.store.SetDeviceData(d.ID, string(payload))
		if s.metrics != nil {
			s.metrics.Telemetry(d.Name, d.ClientID, payload, now)
		}
	}
	if err != nil {
		log.Printf("record telemetry from %s: %v", topic, err)
	}
}

func (s *Service) publish(r *route, payload string) error {
	if r.device == nil {
		return errors.New("bot sem dispositivo configurado")
	}
	return s.mqtt.Publish(r.device.CmdTopic, payload)
}

func (s *Service) send(chat types.JID, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	if err := s.wa.SendText(ctx, chat, text); err != nil {
		log.Printf("send WhatsApp message to %s: %v", chat, err)
	}
}

func (s *Service) conversation(botID int64, key string) (savedConversation, error) {
	var saved savedConversation
	data, err := s.store.Conversation(botID, key)
	if err != nil || data == nil {
		return saved, err
	}
	// A conversation that no longer parses starts over.
	if json.Unmarshal(data, &saved) != nil {
		return savedConversation{}, nil
	}
	return saved, nil
}

func (s *Service) saveConversation(r *route, key string, chat types.JID, c flow.Conversation) {
	var data []byte
	if c.Step != flow.StepIdle {
		data, _ = json.Marshal(savedConversation{Chat: chat.ToNonAD().String(), Conversation: c})
	}
	if err := s.store.SaveConversation(r.bot.ID, key, data); err != nil {
		log.Printf("bot %q: save conversation: %v", r.bot.Name, err)
	}
}

// matchesKeyword reports whether the text contains the keyword, ignoring
// case. An empty keyword accepts any message, including those without text
// (audio, sticker, photo without a caption).
func matchesKeyword(text, keyword string) bool {
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(keyword))
}
