package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pingzap/internal/config"
)

var at = time.Unix(1700000000, 0)

func TestEncodeLine(t *testing.T) {
	got := encodeLine("telemetry",
		map[string]string{"device": "Portão da casa", "client_id": "esp,1", "empty": ""},
		map[string]any{"wifi_rssi": -61.0, "alert": 1.0, "event": `say "hi"`},
		at)
	want := `telemetry,client_id=esp\,1,device=Portão\ da\ casa alert=1,event="say \"hi\"",wifi_rssi=-61 1700000000000000000`
	if got != want {
		t.Errorf("encodeLine =\n%s\nwant\n%s", got, want)
	}
	if encodeLine("telemetry", nil, map[string]any{}, at) != "" {
		t.Error("wrote a line without fields")
	}
}

func TestTelemetryAndStatus(t *testing.T) {
	bodies := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret" || r.URL.Query().Get("bucket") != "pingzap" {
			t.Errorf("request = %s %v", r.URL, r.Header)
		}
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	w := New(config.Influx{URL: srv.URL, Token: "secret", Org: "pingzap", Bucket: "pingzap"})
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	defer cancel()

	w.Telemetry("Portão", "esp-1", []byte(`{"device":"esp-1","uptime_s":12,"alert":true,"relay":false,"event":"ON","extra":{"x":1}}`), at)
	w.Telemetry("Portão", "esp-1", []byte(`not json`), at)
	w.Status("Portão", "esp-1", "offline", at)

	select {
	case body := <-bodies:
		lines := strings.Split(body, "\n")
		want := []string{
			`telemetry,client_id=esp-1,device=Portão alert=1,event="ON",relay=0,uptime_s=12 1700000000000000000`,
			`status,client_id=esp-1,device=Portão online=0 1700000000000000000`,
		}
		if strings.Join(lines, "\n") != strings.Join(want, "\n") {
			t.Errorf("body =\n%s\nwant\n%s", body, strings.Join(want, "\n"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing written")
	}
}

func TestDisabled(t *testing.T) {
	if New(config.Influx{}) != nil {
		t.Error("writer created without a URL")
	}
}
