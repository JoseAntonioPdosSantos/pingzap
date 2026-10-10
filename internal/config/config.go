// Package config loads the pingzap configuration from environment variables,
// with an optional .env file in the working directory.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

const envFile = ".env"

type Config struct {
	MQTT     MQTT
	WhatsApp WhatsApp
}

type MQTT struct {
	BrokerURL string
	// Username and Password are optional; public brokers need neither.
	Username  string
	Password  string
	DataTopic string
	CmdTopic  string
	// OnPayload starts the alert on the ESP32 and OffPayload ends it.
	OnPayload  string
	OffPayload string
}

type WhatsApp struct {
	// DB is the SQLite DSN holding the session; delete the file to pair again.
	DB string
	// Numbers are the phones whose messages trigger the alert.
	Numbers []string
	// Keyword restricts the trigger to messages containing it. When empty,
	// any message from the configured numbers triggers.
	Keyword string
}

func Load() (Config, error) {
	if err := loadEnvFile(envFile); err != nil {
		return Config{}, err
	}

	cfg := Config{
		MQTT: MQTT{
			// Port 8084 on broker.emqx.io is MQTT over secure WebSocket (WSS).
			BrokerURL:  env("PINGZAP_MQTT_BROKER", "wss://broker.emqx.io:8084/mqtt"),
			Username:   env("PINGZAP_MQTT_USERNAME", ""),
			Password:   os.Getenv("PINGZAP_MQTT_PASSWORD"),
			DataTopic:  env("PINGZAP_MQTT_DATA_TOPIC", "esp32/unit01/data"),
			CmdTopic:   env("PINGZAP_MQTT_CMD_TOPIC", "esp32/unit01/cmd"),
			OnPayload:  env("PINGZAP_MQTT_ON_PAYLOAD", "ON"),
			OffPayload: env("PINGZAP_MQTT_OFF_PAYLOAD", "OFF"),
		},
		WhatsApp: WhatsApp{
			DB:      env("PINGZAP_WHATSAPP_DB", "file:whatsapp.db?_foreign_keys=on"),
			Keyword: strings.TrimSpace(os.Getenv("PINGZAP_KEYWORD")),
		},
	}

	for _, n := range strings.Split(os.Getenv("PINGZAP_NUMBERS"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			cfg.WhatsApp.Numbers = append(cfg.WhatsApp.Numbers, n)
		}
	}
	if len(cfg.WhatsApp.Numbers) == 0 {
		return Config{}, errors.New("PINGZAP_NUMBERS is empty: set at least one number (area code + phone), comma-separated")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// loadEnvFile reads KEY=value lines and exports them. Variables already set
// in the environment take precedence; a missing file is not an error.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=value", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}
