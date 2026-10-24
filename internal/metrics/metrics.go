// Package metrics writes the devices' telemetry and status to InfluxDB, where
// Grafana reads it. Writes are batched in the background so a slow or
// offline database never holds up MQTT.
package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"pingzap/internal/config"
)

const (
	flushInterval = 2 * time.Second
	maxBatch      = 500
	// queueSize bounds memory while InfluxDB is down; newer points are
	// dropped once it fills.
	queueSize = 5000
)

type Writer struct {
	writeURL string
	token    string
	http     *http.Client
	lines    chan string
}

// New returns nil when InfluxDB is not configured.
func New(cfg config.Influx) *Writer {
	if cfg.URL == "" {
		return nil
	}
	q := url.Values{"org": {cfg.Org}, "bucket": {cfg.Bucket}, "precision": {"ns"}}
	return &Writer{
		writeURL: strings.TrimSuffix(cfg.URL, "/") + "/api/v2/write?" + q.Encode(),
		token:    cfg.Token,
		http:     &http.Client{Timeout: 10 * time.Second},
		lines:    make(chan string, queueSize),
	}
}

// Telemetry records a telemetry message: every number and boolean of the
// JSON object becomes a field (booleans as 0/1, so they can be graphed) and
// strings become text fields, e.g. the event that triggered the message.
func (w *Writer) Telemetry(device, clientID string, payload []byte, at time.Time) {
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		log.Printf("metrics: telemetry from %s is not a JSON object; skipped", device)
		return
	}
	tags := map[string]string{"device": device, "client_id": clientID}
	fields := make(map[string]any)
	for k, v := range data {
		if _, isTag := tags[k]; isTag {
			continue
		}
		switch v := v.(type) {
		case float64, string:
			fields[k] = v
		case bool:
			fields[k] = boolFloat(v)
		}
	}
	w.enqueue(encodeLine("telemetry", tags, fields, at))
}

// Status records the online/offline status a device publishes.
func (w *Writer) Status(device, clientID, status string, at time.Time) {
	tags := map[string]string{"device": device, "client_id": clientID}
	fields := map[string]any{"online": boolFloat(status == "online")}
	w.enqueue(encodeLine("status", tags, fields, at))
}

func (w *Writer) enqueue(line string) {
	if line == "" {
		return
	}
	select {
	case w.lines <- line:
	default:
		log.Println("metrics: queue full; point dropped")
	}
}

// Run sends the queued points until ctx ends.
func (w *Writer) Run(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	var batch []string
	for {
		select {
		case <-ctx.Done():
			w.flush(batch)
			return
		case line := <-w.lines:
			batch = append(batch, line)
			if len(batch) >= maxBatch {
				batch = w.flush(batch)
			}
		case <-ticker.C:
			batch = w.flush(batch)
		}
	}
}

// flush sends the batch and returns what is left to send: nothing on
// success, the same batch when InfluxDB is unreachable (capped, so a long
// outage keeps only the newest points).
func (w *Writer) flush(batch []string) []string {
	if len(batch) == 0 {
		return batch
	}
	err := w.write(strings.Join(batch, "\n"))
	if err == nil {
		return batch[:0]
	}
	log.Printf("metrics: %v", err)
	if len(batch) > queueSize {
		batch = slices.Clone(batch[len(batch)-queueSize:])
	}
	return batch
}

func (w *Writer) write(body string) error {
	req, err := http.NewRequest(http.MethodPost, w.writeURL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+w.token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("write to InfluxDB: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("write to InfluxDB: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// encodeLine builds an InfluxDB line protocol entry. Numbers are always
// written as floats so a field never changes type between points. It
// returns "" when there is no field to write.
func encodeLine(measurement string, tags map[string]string, fields map[string]any, at time.Time) string {
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(escape(measurement, ", "))
	for _, k := range sortedKeys(tags) {
		if tags[k] == "" {
			continue
		}
		fmt.Fprintf(&b, ",%s=%s", escape(k, ",= "), escape(tags[k], ",= "))
	}
	for i, k := range sortedKeys(fields) {
		sep := ","
		if i == 0 {
			sep = " "
		}
		b.WriteString(sep + escape(k, ",= ") + "=")
		switch v := fields[k].(type) {
		case float64:
			b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
		case string:
			b.WriteString(`"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`)
		}
	}
	fmt.Fprintf(&b, " %d", at.UnixNano())
	return b.String()
}

// escape backslash-escapes the given characters, as line protocol requires
// for measurements, tag keys and values, and field keys.
func escape(s, chars string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if strings.ContainsRune(chars, r) || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
