/*
 * pingzap - ESP32
 *
 * Fetches its MQTT client ID and topics from the pingzap API, then:
 * - listens on the command topic: "ON" / "OFF" start and end the alert (LED
 *   and buzzer), "PULSE" drives the gate once (D5 and the relay on D23);
 * - publishes telemetry to the data topic every 5 s and on every command,
 *   always with the same JSON fields (see publishTelemetry);
 * - reports online / offline on the status topic (LWT, retained);
 * - blinks the LED 3 times whenever it connects to the WiFi.
 */

#include <WiFi.h>
#include <HTTPClient.h>
#include <PubSubClient.h>
#include <ArduinoJson.h>

// WiFi credentials (ssid / password), the broker address (mqtt_server), the
// MQTT login (mqtt_user / mqtt_pass) and the pingzap API address and device
// token (api_url / device_token) live in secrets.h, which is not versioned.
// Copy secrets_example.h to secrets.h and fill it in.
#include "secrets.h"

const int mqtt_port = 1883;

// MQTT client ID and topics come from the pingzap API (Dispositivos page), so
// they are managed in a single place. The board checks for changes every
// CONFIG_REFRESH ms and reconnects when they change.
String device_id;       // Unique on the broker
String topic_command;   // Where we listen for commands
String topic_telemetry; // Where we send telemetry
String topic_status;    // LWT (online / offline)
unsigned long lastConfigFetch = 0;
const unsigned long CONFIG_REFRESH = 60000;

WiFiClient espClient;
PubSubClient client(espClient);

// Telemetry timer
unsigned long lastMsgTime = 0;
const unsigned long TELEMETRY_INTERVAL = 5000;

#define LED_PIN 2 // Onboard LED
#define BUZZER_PIN 4 // D4 -> buzzer (+); buzzer (-) -> GND
#define GATE_PIN 5 // D5 -> Gate (HIGH during the gate pulse)
#define RELAY_PIN 23 // D23 -> resistor 1k -> base do BC547 (rele)

// Pulso do portao (nao bloqueante): um unico pulso por comando "PULSE", com
// D5 e D23 em HIGH juntos, como um aperto no botao do portao.
bool relayPulseActive = false;
unsigned long relayPulseStart = 0;
const unsigned long RELAY_PULSE_MS = 1000; // tempo que D5 e D23 ficam em HIGH

// Passive buzzer tone, in Hz
const unsigned int BUZZER_FREQ = 2000;

// Alert blink (non-blocking): LED and buzzer toggle until "OFF" arrives
bool alertActive = false;
unsigned long lastBlink = 0;
const unsigned long BLINK_INTERVAL = 150;

// WiFi connection signal: the LED blinks this many times on connecting
const int CONNECT_BLINKS = 3;
const unsigned long CONNECT_BLINK_MS = 150;

void setBuzzer(bool on) {
  if (on) {
    tone(BUZZER_PIN, BUZZER_FREQ);
  } else {
    noTone(BUZZER_PIN);
  }
}

// Blinks the LED to show the WiFi is connected. It only runs right after
// connecting, so a short blocking pause is fine; the alert LED state is
// restored afterwards.
void blinkConnected() {
  bool ledWasOn = digitalRead(LED_PIN);
  for (int i = 0; i < CONNECT_BLINKS; i++) {
    digitalWrite(LED_PIN, HIGH);
    delay(CONNECT_BLINK_MS);
    digitalWrite(LED_PIN, LOW);
    delay(CONNECT_BLINK_MS);
  }
  digitalWrite(LED_PIN, ledWasOn);
}

void setup_wifi() {
  Serial.println();
  Serial.print("Connecting to WiFi: ");
  Serial.println(ssid);

  WiFi.mode(WIFI_STA);
  WiFi.begin(ssid, password);

  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
  }

  Serial.println("");
  Serial.println("WiFi connected");
  Serial.print("IP address: ");
  Serial.println(WiFi.localIP());
  blinkConnected();
}

// Waits for the WiFi to come back after a drop (the ESP32 reconnects by
// itself) and signals it with the connection blink.
void ensureWifi() {
  if (WiFi.status() == WL_CONNECTED) {
    return;
  }
  Serial.print("WiFi lost, reconnecting");
  setBuzzer(false); // don't leave a steady tone while offline
  WiFi.reconnect();
  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
  }
  Serial.println("");
  Serial.println("WiFi reconnected");
  blinkConnected();
}

// Publishes the board state, always with the same fields so the data can
// be graphed (pingzap stores it in InfluxDB for Grafana):
//   uptime_s   seconds since boot
//   wifi_rssi  WiFi signal, in dBm
//   free_heap  free memory, in bytes
//   alert      alert on (LED and buzzer)
//   relay      gate pulse in progress (D5 and D23)
//   event      the command that caused this message; absent on the
//              periodic report
void publishTelemetry(const char* event) {
  JsonDocument doc;
  doc["device"] = device_id;
  doc["uptime_s"] = millis() / 1000;
  doc["wifi_rssi"] = WiFi.RSSI();
  doc["free_heap"] = ESP.getFreeHeap();
  doc["alert"] = alertActive;
  doc["relay"] = relayPulseActive;
  if (event) {
    doc["event"] = event;
  }

  char buffer[256];
  serializeJson(doc, buffer);
  Serial.print("Publishing data: ");
  Serial.println(buffer);
  client.publish(topic_telemetry.c_str(), buffer);
}

// Asks pingzap which client ID and topics this board uses. Returns true when
// the configuration was read; *changed tells whether it differs from before.
bool fetchConfig(bool* changed) {
  HTTPClient http;
  http.begin(String(api_url) + "/api/device/config");
  http.addHeader("Authorization", String("Bearer ") + device_token);
  int code = http.GET();
  if (code != HTTP_CODE_OK) {
    Serial.print("Config fetch failed, HTTP ");
    Serial.println(code);
    http.end();
    return false;
  }

  JsonDocument doc;
  DeserializationError err = deserializeJson(doc, http.getString());
  http.end();
  if (err) {
    Serial.print("Config parse failed: ");
    Serial.println(err.c_str());
    return false;
  }

  String id = doc["client_id"] | "";
  String cmd = doc["cmd_topic"] | "";
  String data = doc["data_topic"] | "";
  String status = doc["status_topic"] | "";
  if (id == "" || cmd == "" || data == "" || status == "") {
    Serial.println("Config incomplete");
    return false;
  }

  *changed = id != device_id || cmd != topic_command || data != topic_telemetry || status != topic_status;
  if (*changed) {
    device_id = id;
    topic_command = cmd;
    topic_telemetry = data;
    topic_status = status;
    Serial.println("Config: client " + device_id + ", cmd " + topic_command +
                   ", data " + topic_telemetry + ", status " + topic_status);
  }
  return true;
}

// Handles the commands published by pingzap.
void callback(char* topic, byte* payload, unsigned int length) {
  String message;
  for (unsigned int i = 0; i < length; i++) {
    message += (char)payload[i];
  }
  Serial.print("Message arrived [");
  Serial.print(topic);
  Serial.print("] ");
  Serial.println(message);

  if (topic_command != topic) {
    return;
  }
  if (message == "ON") {
    alertActive = true;
    lastBlink = millis();
    digitalWrite(LED_PIN, HIGH);
    setBuzzer(true);
    publishTelemetry("ON");
  } else if (message == "OFF") {
    alertActive = false;
    digitalWrite(LED_PIN, LOW);
    setBuzzer(false);
    publishTelemetry("OFF");
  } else if (message == "PULSE") {
    // Um pulso no D5 e no D23 para este comando (desligado sozinho no loop)
    digitalWrite(GATE_PIN, HIGH);
    digitalWrite(RELAY_PIN, HIGH);
    relayPulseActive = true;
    relayPulseStart = millis();
    publishTelemetry("PULSE");
  }
}

void reconnect() {
  while (!client.connected()) {
    ensureWifi(); // the broker is unreachable without WiFi
    Serial.print("Attempting MQTT connection...");
    // Last Will: if this ESP32 drops, the broker publishes "offline" (retained).
    if (client.connect(device_id.c_str(), mqtt_user, mqtt_pass, topic_status.c_str(), 1, true, "offline")) {
      Serial.println("connected");
      client.publish(topic_status.c_str(), "online", true);
      client.subscribe(topic_command.c_str());
    } else {
      Serial.print("failed, rc=");
      Serial.print(client.state());
      Serial.println(" try again in 5 seconds");
      delay(5000); // Nothing to do without the broker
    }
  }
}

void setup() {
  Serial.begin(115200);
  pinMode(LED_PIN, OUTPUT);
  digitalWrite(LED_PIN, LOW);
  pinMode(BUZZER_PIN, OUTPUT);
  digitalWrite(BUZZER_PIN, LOW);
  pinMode(GATE_PIN, OUTPUT);
  digitalWrite(GATE_PIN, LOW);
  pinMode(RELAY_PIN, OUTPUT);
  digitalWrite(RELAY_PIN, LOW); // rele desligado ao ligar o ESP32

  setup_wifi();

  // Without its topics the board has nothing to do: retry until pingzap answers.
  bool changed;
  while (!fetchConfig(&changed)) {
    delay(5000);
  }
  lastConfigFetch = millis();

  client.setServer(mqtt_server, mqtt_port);
  client.setCallback(callback);
}

void loop() {
  ensureWifi();
  if (!client.connected()) {
    reconnect();
  }
  client.loop();

  unsigned long now = millis();

  // Topics edited in pingzap take effect here
  if (now - lastConfigFetch >= CONFIG_REFRESH) {
    lastConfigFetch = now;
    bool changed = false;
    if (fetchConfig(&changed) && changed) {
      // Reconnecting subscribes to the new topics and sets the new LWT.
      client.disconnect();
    }
  }

  // Desliga o D5 e o D23 apos RELAY_PULSE_MS
  if (relayPulseActive && now - relayPulseStart >= RELAY_PULSE_MS) {
    digitalWrite(GATE_PIN, LOW);
    digitalWrite(RELAY_PIN, LOW);
    relayPulseActive = false;
  }

  // Alert blink: LED and buzzer together
  if (alertActive && now - lastBlink >= BLINK_INTERVAL) {
    lastBlink = now;
    digitalWrite(LED_PIN, !digitalRead(LED_PIN));
    setBuzzer(digitalRead(LED_PIN));
  }

  // Periodic telemetry
  if (now - lastMsgTime >= TELEMETRY_INTERVAL) {
    lastMsgTime = now;
    publishTelemetry(nullptr);
  }
}
