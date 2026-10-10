# pingzap

**English** | [Português (Brasil)](README.pt-BR.md)

A bridge between WhatsApp and an ESP32: when one of the configured numbers
sends me a message, pingzap publishes `ON` to an MQTT broker and the ESP32,
subscribed to that topic, blinks its LED as an urgency signal. When I reply to
that person, pingzap publishes `OFF` and the LED turns off.

## Flow

```
 contact               pingzap (Go)                MQTT broker             ESP32
    │                       │                           │                     │
    │── message ───────────▶│                           │                     │
    │   (WhatsApp)          │ 1. is it from a           │                     │
    │                       │    configured number?     │                     │
    │                       │ 2. does it contain the    │                     │
    │                       │    keyword (if any)?      │                     │
    │                       │── "ON" to ───────────────▶│── "ON" ────────────▶│
    │                       │   esp32/unit01/cmd        │                     │ LED blinking
    │                       │                           │                     │
    │◀── my reply ──────────│ 3. has everyone who       │                     │
    │    (from any          │    triggered it been      │                     │
    │     device)           │    answered?              │                     │
    │                       │── "OFF" to ──────────────▶│── "OFF" ───────────▶│
    │                       │   esp32/unit01/cmd        │                     │ LED off
    │                       │                           │                     │
    │                       │◀── esp32/unit01/data ─────│◀── LED state ───────│
    │                       │    (logged only)          │    + telemetry      │
```

1. pingzap connects to WhatsApp as a **linked device** (the same mechanism as
   WhatsApp Web), using the [whatsmeow](https://github.com/tulir/whatsmeow)
   library. The session is stored in `whatsapp.db`.
2. **Turning on (`ON`)** — two filters are applied to every incoming message:
   - **Number**: the sender must be listed in `PINGZAP_NUMBERS`. Group
     messages are ignored.
   - **Keyword**: if `PINGZAP_KEYWORD` is set (e.g. `/urgente`), the message
     must contain it. If it is empty, any message passes.

   When both pass, pingzap publishes `ON` to the command topic
   (`esp32/unit01/cmd`) with QoS 1 and marks the number as awaiting a reply.
3. **Turning off (`OFF`)** — when I send a message to one of the configured
   numbers, from my phone or any other linked device, that number stops
   awaiting a reply. If no number is left waiting, pingzap publishes `OFF`. So
   if two people triggered the alert, the LED only turns off after I reply to
   both. Any message of mine counts as a reply; the keyword does not apply here.
4. The ESP32 (sketch at [`firmware/pingzap/pingzap.ino`](firmware/pingzap/pingzap.ino))
   blinks the onboard LED on `ON` and turns it off on `OFF`. On every change it
   reports the state to the telemetry topic (`esp32/unit01/data`), which
   pingzap simply writes to the log.

## Architecture

```
pingzap/
├── cmd/
│   └── pingzap/
│       └── main.go          # entry point: loads the config and wires the parts
├── internal/
│   ├── alert/
│   │   ├── alert.go         # tracks which numbers are still awaiting a reply
│   │   └── alert_test.go
│   ├── config/
│   │   └── config.go        # reads .env / environment variables and validates
│   ├── mqtt/
│   │   └── mqtt.go          # broker connection, ON/OFF publishing, telemetry log
│   └── whatsapp/
│       ├── whatsapp.go      # session, QR code pairing and message handling
│       ├── filter.go        # number and keyword rules
│       └── filter_test.go
├── docs/
│   ├── broker.md            # broker setup guide
│   └── broker.pt-BR.md
├── firmware/
│   └── pingzap/
│       ├── pingzap.ino      # ESP32 sketch
│       └── secrets_example.h
├── .env_example
├── README.md
├── README.pt-BR.md
└── go.mod
```

The `whatsapp`, `mqtt` and `alert` packages do not know about each other.
`whatsapp` receives two callbacks — `OnTrigger` (a message that triggers the
alert arrived) and `OnReply` (I replied to a configured number) — and `main.go`
wires them to `alert.Tracker` and `mqtt.PublishAlert` / `mqtt.PublishClear`.

The firmware lives in `firmware/`, outside the Go module: it is a different
toolchain (Arduino), but it stays in the same repository because it shares the
broker, topic and payload contract with pingzap.

## Configuration

Configuration comes from environment variables. To avoid exporting them, create
a `.env` file in the directory the program runs from:

```sh
cp .env_example .env
```

| Variable                   | Default                              | Description |
|----------------------------|--------------------------------------|-------------|
| `PINGZAP_NUMBERS`          | — (required)                         | Numbers that trigger the alert, comma-separated. |
| `PINGZAP_KEYWORD`          | empty                                | Keyword required in the message. Empty = any message triggers. |
| `PINGZAP_MQTT_BROKER`      | `wss://broker.emqx.io:8084/mqtt`     | Broker URL. |
| `PINGZAP_MQTT_USERNAME`    | empty                                | Broker username (not needed on public brokers). |
| `PINGZAP_MQTT_PASSWORD`    | empty                                | Broker password. |
| `PINGZAP_MQTT_CMD_TOPIC`   | `esp32/unit01/cmd`                   | Topic the commands are published to. |
| `PINGZAP_MQTT_DATA_TOPIC`  | `esp32/unit01/data`                  | ESP32 telemetry topic (logged only). |
| `PINGZAP_MQTT_ON_PAYLOAD`  | `ON`                                 | Payload published to start the alert. |
| `PINGZAP_MQTT_OFF_PAYLOAD` | `OFF`                                | Payload published to end the alert. |
| `PINGZAP_WHATSAPP_DB`      | `file:whatsapp.db?_foreign_keys=on`  | SQLite database holding the WhatsApp session. |

Variables already exported in the shell take precedence over the `.env` file.

To use your own broker instead of the public one, or to inspect the topics with
MQTTX, see the [broker setup guide](docs/broker.md).

### Numbers

```sh
PINGZAP_NUMBERS=67999999999,67988888888
```

- The basic format is area code + phone; Brazil's country code (`55`) is added
  automatically. The full number with punctuation, such as
  `+55 (67) 99999-9999`, is also accepted.
- Older Brazilian accounts are identified on WhatsApp without the ninth digit;
  pingzap accepts both forms, so just enter the number as usual.

### Keyword

```sh
PINGZAP_KEYWORD=/urgente   # only messages containing /urgente
PINGZAP_KEYWORD=           # any message
```

- Matching is case-insensitive and the keyword may appear anywhere in the text
  (`Call me, /URGENTE!` triggers).
- It applies to the message text and to photo, video and document captions.
- With a keyword set, messages without text (audio, sticker, photo without a
  caption) do not trigger. Without a keyword, they do.

## Running

Requirements: Go and a C compiler (the SQLite driver uses cgo).

```sh
go run ./cmd/pingzap
```

Or build the binary:

```sh
go build -o pingzap ./cmd/pingzap
./pingzap
```

On the **first run** a QR code is shown in the terminal: on your phone, open
WhatsApp at *Linked devices > Link a device* and scan it. On later runs the
session saved in `whatsapp.db` is reused. To pair another account, delete that
file.

Always run from the repository root, since `.env` and `whatsapp.db` are looked
up in the current directory.

Tests:

```sh
go test ./...
```

## ESP32

The sketch is at `firmware/pingzap/pingzap.ino` and needs the `PubSubClient`
and `ArduinoJson` (v7) libraries.

Before flashing, create the file with the Wi-Fi credentials (and the broker
login, if your broker requires one):

```sh
cp firmware/pingzap/secrets_example.h firmware/pingzap/secrets.h
```

Then open the `firmware/pingzap` folder in the Arduino IDE (the folder and the
`.ino` file must share the same name) and upload it to the board.

### Buzzer

A buzzer beeps along with the LED while the alert is active. Wire its positive
leg to `D4` (GPIO 4) and the other leg to `GND`. The sketch drives a passive
buzzer with a 2 kHz tone; for an active buzzer, set `BUZZER_FREQ` to `0`.

### Simulator

No board at hand? The firmware is available as a
[Wokwi simulation](https://wokwi.com/projects/477507248547038209): open the link and press
play to run a virtual ESP32 in the browser. It connects to the same broker and
topics, so it reacts to the `ON` / `OFF` commands published by pingzap just
like the real board.

### Topics

| Topic                 | Direction        | Content |
|-----------------------|------------------|---------|
| `esp32/unit01/cmd`    | pingzap → ESP32  | `ON` starts the alert (LED blinking); `OFF` ends it. |
| `esp32/unit01/data`   | ESP32 → pingzap  | JSON telemetry every 5 s and the LED state. |
| `esp32/unit01/status` | ESP32 → broker   | `online` / `offline` (LWT, retained). |

The LED blinks until `OFF` arrives. To make it also turn off by itself after a
while, set `ALERT_DURATION` in the sketch (in milliseconds; `0` = no limit).

If you change the topics, payloads or broker in `.env`, change them in the
sketch too — both sides must use the same values. pingzap talks to the broker
over secure WebSocket (port 8084) and the ESP32 over plain MQTT (port 1883); it
is the same broker.

## Notes

- `broker.emqx.io` is a **public** broker: anyone who knows the topic can read
  from or publish to it. For real use, switch to a broker with authentication
  (see the [broker setup guide](docs/broker.md)) or, at the very least, use
  hard-to-guess topics.
- `whatsapp.db` grants access to the WhatsApp account, `.env` holds phone
  numbers and `secrets.h` the Wi-Fi and broker passwords; all three are in `.gitignore` and
  must not be committed.
- Pending alerts are kept in memory only. If pingzap is restarted while the LED
  is blinking, the next reply to any configured number publishes `OFF`.
- Commands are not retained by the broker: if the ESP32 is powered off or
  offline when `ON` or `OFF` is published, it does not receive that command.
