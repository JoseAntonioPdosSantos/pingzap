// Package config loads the pingzap configuration from environment variables,
// with an optional .env file in the working directory.
//
// Only the infrastructure lives here (broker, databases, HTTP address,
// InfluxDB).
// Devices, topics, bots and the chats they answer are managed in the web
// interface.
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
	MQTT MQTT
	// HTTPAddr is where the web interface and the API listen.
	HTTPAddr string
	// DB is the SQLite DSN of the pingzap data (users, devices, bots).
	DB string
	// WhatsAppDB is the SQLite DSN holding the WhatsApp session.
	WhatsAppDB string
	// Influx receives the devices' telemetry for Grafana; disabled when
	// its URL is empty.
	Influx Influx
	// Legacy is the alert configured by environment variables before the
	// web interface existed; it is imported once into the database.
	Legacy Legacy
}

type MQTT struct {
	BrokerURL string
	// Username and Password are optional; public brokers need neither.
	Username string
	Password string
}

type Influx struct {
	URL    string
	Token  string
	Org    string
	Bucket string
}

type Legacy struct {
	Numbers    []string
	Keyword    string
	CmdTopic   string
	DataTopic  string
	OnPayload  string
	OffPayload string
}

func Load() (Config, error) {
	if err := loadEnvFile(envFile); err != nil {
		return Config{}, err
	}

	cfg := Config{
		MQTT: MQTT{
			// Port 8084 on broker.emqx.io is MQTT over secure WebSocket (WSS).
			BrokerURL: env("PINGZAP_MQTT_BROKER", "wss://broker.emqx.io:8084/mqtt"),
			Username:  env("PINGZAP_MQTT_USERNAME", ""),
			Password:  os.Getenv("PINGZAP_MQTT_PASSWORD"),
		},
		HTTPAddr:   env("PINGZAP_HTTP_ADDR", ":8080"),
		DB:         env("PINGZAP_DB", "file:pingzap.db?_foreign_keys=on"),
		WhatsAppDB: env("PINGZAP_WHATSAPP_DB", "file:whatsapp.db?_foreign_keys=on"),
		Influx: Influx{
			URL:    env("PINGZAP_INFLUX_URL", ""),
			Token:  os.Getenv("PINGZAP_INFLUX_TOKEN"),
			Org:    env("PINGZAP_INFLUX_ORG", "pingzap"),
			Bucket: env("PINGZAP_INFLUX_BUCKET", "pingzap"),
		},
		Legacy: Legacy{
			Keyword:    strings.TrimSpace(os.Getenv("PINGZAP_KEYWORD")),
			CmdTopic:   env("PINGZAP_MQTT_CMD_TOPIC", "esp32/unit01/cmd"),
			DataTopic:  env("PINGZAP_MQTT_DATA_TOPIC", "esp32/unit01/data"),
			OnPayload:  env("PINGZAP_MQTT_ON_PAYLOAD", "ON"),
			OffPayload: env("PINGZAP_MQTT_OFF_PAYLOAD", "OFF"),
		},
	}

	for _, n := range strings.Split(os.Getenv("PINGZAP_NUMBERS"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			cfg.Legacy.Numbers = append(cfg.Legacy.Numbers, n)
		}
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
