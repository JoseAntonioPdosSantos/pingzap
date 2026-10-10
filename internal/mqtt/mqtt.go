// Package mqtt connects to the broker, publishes the alert commands and logs
// the telemetry sent back by the ESP32.
package mqtt

import (
	"fmt"
	"log"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"pingzap/internal/config"
)

const (
	qos            = 1
	publishTimeout = 10 * time.Second
)

type Client struct {
	cfg  config.MQTT
	paho paho.Client
}

func Connect(cfg config.MQTT) (*Client, error) {
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
	opts.SetOnConnectHandler(func(c paho.Client) {
		log.Printf("connected to %s", cfg.BrokerURL)
		token := c.Subscribe(cfg.DataTopic, qos, onMessage)
		token.Wait()
		if err := token.Error(); err != nil {
			log.Printf("failed to subscribe to %s: %v", cfg.DataTopic, err)
			return
		}
		log.Printf("listening on topic %s", cfg.DataTopic)
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		log.Printf("connection lost: %v", err)
	})

	client := paho.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("connect to the broker: %w", token.Error())
	}
	return &Client{cfg: cfg, paho: client}, nil
}

// PublishAlert sends the command that makes the ESP32 blink the LED.
func (c *Client) PublishAlert() error {
	return c.publish(c.cfg.OnPayload)
}

// PublishClear sends the command that turns the LED off.
func (c *Client) PublishClear() error {
	return c.publish(c.cfg.OffPayload)
}

func (c *Client) publish(payload string) error {
	token := c.paho.Publish(c.cfg.CmdTopic, qos, false, payload)
	if !token.WaitTimeout(publishTimeout) {
		return fmt.Errorf("publish to %s: timed out", c.cfg.CmdTopic)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish to %s: %w", c.cfg.CmdTopic, err)
	}
	log.Printf("published %q to %s", payload, c.cfg.CmdTopic)
	return nil
}

func (c *Client) Disconnect() {
	c.paho.Disconnect(250)
}

func onMessage(_ paho.Client, msg paho.Message) {
	log.Printf("[%s] %s", msg.Topic(), msg.Payload())
}
