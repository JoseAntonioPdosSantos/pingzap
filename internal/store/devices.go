package store

import (
	"database/sql"
	"strings"
	"time"
)

// Device is an ESP32 and the MQTT topics it uses. The board fetches its
// topics from the API with Token, so they are managed only here.
type Device struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// ClientID is the board's MQTT client ID; it must be unique on the broker.
	ClientID    string `json:"client_id"`
	Token       string `json:"token"`
	CmdTopic    string `json:"cmd_topic"`
	DataTopic   string `json:"data_topic"`
	StatusTopic string `json:"status_topic"`
	// Status, LastSeen and LastData come from the board over MQTT.
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen,omitzero"`
	LastData string    `json:"last_data"`
}

const deviceColumns = `id, name, client_id, token, cmd_topic, data_topic, status_topic, status, last_seen, last_data`

func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	var lastSeen int64
	err := row.Scan(&d.ID, &d.Name, &d.ClientID, &d.Token, &d.CmdTopic, &d.DataTopic, &d.StatusTopic,
		&d.Status, &lastSeen, &d.LastData)
	if lastSeen > 0 {
		d.LastSeen = time.Unix(lastSeen, 0)
	}
	return d, err
}

func (d *Device) validate() error {
	d.Name = strings.TrimSpace(d.Name)
	d.ClientID = strings.TrimSpace(d.ClientID)
	d.CmdTopic = strings.TrimSpace(d.CmdTopic)
	d.DataTopic = strings.TrimSpace(d.DataTopic)
	d.StatusTopic = strings.TrimSpace(d.StatusTopic)
	if d.Name == "" || d.ClientID == "" {
		return invalid("informe o nome e o client ID do dispositivo")
	}
	if d.CmdTopic == "" || d.DataTopic == "" || d.StatusTopic == "" {
		return invalid("informe os três tópicos do dispositivo")
	}
	for _, t := range []string{d.CmdTopic, d.DataTopic, d.StatusTopic} {
		if strings.ContainsAny(t, "#+") {
			return invalid("os tópicos não podem ter curingas (# ou +)")
		}
	}
	if d.CmdTopic == d.DataTopic || d.CmdTopic == d.StatusTopic || d.DataTopic == d.StatusTopic {
		return invalid("os três tópicos precisam ser diferentes")
	}
	return nil
}

func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT ` + deviceColumns + ` FROM devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (s *Store) GetDevice(id int64) (Device, error) {
	d, err := scanDevice(s.db.QueryRow(`SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id))
	return d, notFound(err)
}

func (s *Store) DeviceByToken(token string) (Device, error) {
	if token == "" {
		return Device{}, ErrNotFound
	}
	d, err := scanDevice(s.db.QueryRow(`SELECT `+deviceColumns+` FROM devices WHERE token = ?`, token))
	return d, notFound(err)
}

func (s *Store) CreateDevice(d Device) (Device, error) {
	if err := d.validate(); err != nil {
		return Device{}, err
	}
	d.Token = randomToken()
	res, err := s.db.Exec(`
		INSERT INTO devices (name, client_id, token, cmd_topic, data_topic, status_topic)
		VALUES (?, ?, ?, ?, ?, ?)`,
		d.Name, d.ClientID, d.Token, d.CmdTopic, d.DataTopic, d.StatusTopic)
	if isUnique(err) {
		return Device{}, ErrConflict
	}
	if err != nil {
		return Device{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetDevice(id)
}

func (s *Store) UpdateDevice(d Device) (Device, error) {
	if err := d.validate(); err != nil {
		return Device{}, err
	}
	res, err := s.db.Exec(`
		UPDATE devices SET name = ?, client_id = ?, cmd_topic = ?, data_topic = ?, status_topic = ?
		WHERE id = ?`,
		d.Name, d.ClientID, d.CmdTopic, d.DataTopic, d.StatusTopic, d.ID)
	if isUnique(err) {
		return Device{}, ErrConflict
	}
	if err != nil {
		return Device{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Device{}, ErrNotFound
	}
	return s.GetDevice(d.ID)
}

func (s *Store) RegenerateDeviceToken(id int64) (Device, error) {
	res, err := s.db.Exec(`UPDATE devices SET token = ? WHERE id = ?`, randomToken(), id)
	if err != nil {
		return Device{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Device{}, ErrNotFound
	}
	return s.GetDevice(id)
}

func (s *Store) DeleteDevice(id int64) error {
	res, err := s.db.Exec(`DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDeviceStatus records the online/offline status the board publishes.
func (s *Store) SetDeviceStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE devices SET status = ?, last_seen = ? WHERE id = ?`, status, time.Now().Unix(), id)
	return err
}

// SetDeviceData records the last telemetry message from the board.
func (s *Store) SetDeviceData(id int64, data string) error {
	_, err := s.db.Exec(`UPDATE devices SET last_data = ?, last_seen = ? WHERE id = ?`, data, time.Now().Unix(), id)
	return err
}

// deviceExists reports whether the device ID refers to an existing device.
func deviceExists(tx *sql.Tx, id int64) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM devices WHERE id = ?`, id).Scan(&n)
	return n > 0, err
}
