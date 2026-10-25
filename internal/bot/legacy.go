package bot

import (
	"encoding/json"
	"log"
	"strings"

	"pingzap/internal/config"
	"pingzap/internal/phone"
	"pingzap/internal/store"
)

// ImportLegacy turns the alert configured by environment variables
// (PINGZAP_NUMBERS and friends) into a device and an alert bot, once: only
// while the database has no devices and no bots yet.
func ImportLegacy(st *store.Store, l config.Legacy) error {
	if len(l.Numbers) == 0 {
		return nil
	}
	bots, err := st.ListBots()
	if err != nil {
		return err
	}
	devices, err := st.ListDevices()
	if err != nil {
		return err
	}
	if len(bots) > 0 || len(devices) > 0 {
		return nil
	}

	device, err := st.CreateDevice(store.Device{
		Name:        "ESP32",
		ClientID:    "ESP32_Pro_Unit_01",
		CmdTopic:    l.CmdTopic,
		DataTopic:   l.DataTopic,
		StatusTopic: strings.TrimSuffix(l.CmdTopic, "/cmd") + "/status",
	})
	if err != nil {
		return err
	}

	cfg, _ := json.Marshal(AlertConfig{Keyword: l.Keyword, OnPayload: l.OnPayload, OffPayload: l.OffPayload})
	b := store.Bot{Name: "Alerta", Kind: store.KindAlert, Enabled: true, DeviceID: &device.ID, Config: cfg}
	seen := make(map[string]bool)
	for _, n := range l.Numbers {
		if v := phone.Normalize(n); v != "" && !seen[v] {
			seen[v] = true
			b.Targets = append(b.Targets, store.Target{Kind: store.TargetContact, Value: v, Label: n})
		}
	}
	if _, err := st.SaveBot(b); err != nil {
		return err
	}
	log.Printf("imported PINGZAP_NUMBERS into the alert bot %q; manage it in the web interface from now on", b.Name)
	return nil
}
