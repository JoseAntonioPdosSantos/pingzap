// Package mqtt connects to the broker, publishes commands and delivers the
// messages of the subscribed topics (telemetry and status sent back by the
// devices).
package mqtt

import (
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"pingzap/internal/config"
)

const (
	qos            = 1
	publishTimeout = 10 * time.Second
)

type Client struct {
	paho      paho.Client
	onMessage func(topic string, payload []byte)

	mu     sync.Mutex
	topics []string
}

// Connect starts connecting in the background, retrying until the broker
// answers, so the rest of pingzap runs even while the broker is down.
func Connect(cfg config.MQTT, onMessage func(topic string, payload []byte)) *Client {
	c := &Client{onMessage: onMessage}
	opts := paho.NewClientOptions().
		AddBroker(cfg.BrokerURL).
		SetClientID(fmt.Sprintf("pingzap-%d", time.Now().UnixNano())).
		SetKeepAlive(30 * time.Second).
		SetConnectTimeout(10 * time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)
	if cfg.Username != "" {
		opts.SetUsername(cfg.Username).SetPassword(cfg.Password)
	}

	// Subscribing happens in OnConnect so it is redone on every reconnection.
	opts.SetOnConnectHandler(func(pc paho.Client) {
		log.Printf("connected to %s", cfg.BrokerURL)
		c.mu.Lock()
		topics := slices.Clone(c.topics)
		c.mu.Unlock()
		c.subscribe(pc, topics)
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		log.Printf("connection lost: %v", err)
	})

	c.paho = paho.NewClient(opts)
	c.paho.Connect()
	return c
}

// SetSubscriptions replaces the subscribed topics.
func (c *Client) SetSubscriptions(topics []string) {
	topics = slices.Clone(topics)
	slices.Sort(topics)
	topics = slices.Compact(topics)

	c.mu.Lock()
	var added, removed []string
	for _, t := range topics {
		if !slices.Contains(c.topics, t) {
			added = append(added, t)
		}
	}
	for _, t := range c.topics {
		if !slices.Contains(topics, t) {
			removed = append(removed, t)
		}
	}
	c.topics = topics
	c.mu.Unlock()

	// While offline, OnConnect subscribes to the new list.
	if !c.paho.IsConnectionOpen() {
		return
	}
	if len(removed) > 0 {
		c.paho.Unsubscribe(removed...).WaitTimeout(publishTimeout)
	}
	c.subscribe(c.paho, added)
}

func (c *Client) subscribe(pc paho.Client, topics []string) {
	for _, topic := range topics {
		token := pc.Subscribe(topic, qos, func(_ paho.Client, msg paho.Message) {
			c.onMessage(msg.Topic(), msg.Payload())
		})
		if !token.WaitTimeout(publishTimeout) || token.Error() != nil {
			log.Printf("failed to subscribe to %s: %v", topic, token.Error())
			continue
		}
		log.Printf("listening on topic %s", topic)
	}
}

func (c *Client) Publish(topic, payload string) error {
	token := c.paho.Publish(topic, qos, false, payload)
	if !token.WaitTimeout(publishTimeout) {
		return fmt.Errorf("publish to %s: timed out", topic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	log.Printf("published %q to %s", payload, topic)
	return nil
}

func (c *Client) Disconnect() {
	c.paho.Disconnect(250)
}
