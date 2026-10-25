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
 *
 * WiFi and server settings are not fixed in the code: when the board finds no
 * known network it opens its own network "pingzap-XXXX" with a setup page
 * (WiFiManager), where you pick the WiFi and fill in the broker, the pingzap
 * API and the device token. Holding the BOOT button for 3 s opens the same
 * page at any time. The settings are kept in flash.
 */

#include <WiFi.h>
#include <HTTPClient.h>
#include <PubSubClient.h>
#include <ArduinoJson.h>
#include <Preferences.h>
#include <WiFiManager.h>

// Defaults for the setup page (mqtt_server, mqtt_user, mqtt_pass, api_url,
// device_token) and its password (PORTAL_PASSWORD) live in secrets.h, which
// is not versioned. Copy secrets_example.h to secrets.h and fill it in.
#include "secrets.h"

// Password of the setup network; at least 8 characters.
#ifndef PORTAL_PASSWORD
#define PORTAL_PASSWORD "pingzap123"
#endif

// Settings saved by the setup page; the values from secrets.h are used until
// the page saves something.
struct Settings {
  String mqttServer;
  String mqttUser;
  String mqttPass;
  String apiUrl;
  String deviceToken;
};
Settings cfg;
Preferences prefs;

// Setup page: BOOT held for PORTAL_HOLD_MS opens it; it closes by itself
// after PORTAL_TIMEOUT_S without use.
#define BOOT_PIN 0
const unsigned long PORTAL_HOLD_MS = 3000;
const unsigned long PORTAL_TIMEOUT_S = 180;
unsigned long bootPressedAt = 0;

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

void loadSettings() {
  prefs.begin("pingzap", true);
  cfg.mqttServer = prefs.getString("mqtt_server", mqtt_server);
  cfg.mqttUser = prefs.getString("mqtt_user", mqtt_user);
  cfg.mqttPass = prefs.getString("mqtt_pass", mqtt_pass);
  cfg.apiUrl = prefs.getString("api_url", api_url);
  cfg.deviceToken = prefs.getString("device_token", device_token);
  prefs.end();
}

void saveSettings() {
  prefs.begin("pingzap", false);
  prefs.putString("mqtt_server", cfg.mqttServer);
  prefs.putString("mqtt_user", cfg.mqttUser);
  prefs.putString("mqtt_pass", cfg.mqttPass);
  prefs.putString("api_url", cfg.apiUrl);
  prefs.putString("device_token", cfg.deviceToken);
  prefs.end();
}

// Connects to the saved WiFi or, failing that (or when onDemand), opens the
// setup network. The LED stays on while the page is open. Returns whether
// the WiFi is connected.
bool runPortal(bool onDemand) {
  WiFiManager wm;
  WiFiManagerParameter pServer("mqtt_server", "Broker MQTT (IP ou nome)", cfg.mqttServer.c_str(), 64);
  WiFiManagerParameter pUser("mqtt_user", "Usuario MQTT", cfg.mqttUser.c_str(), 32);
  WiFiManagerParameter pPass("mqtt_pass", "Senha MQTT", cfg.mqttPass.c_str(), 64, "type='password'");
  WiFiManagerParameter pApi("api_url", "API do pingzap (ex.: http://192.168.0.10:8080)", cfg.apiUrl.c_str(), 96);
  WiFiManagerParameter pToken("device_token", "Token do dispositivo (pagina Dispositivos)", cfg.deviceToken.c_str(), 96);
  wm.addParameter(&pServer);
  wm.addParameter(&pUser);
  wm.addParameter(&pPass);
  wm.addParameter(&pApi);
  wm.addParameter(&pToken);

  wm.setTitle("pingzap");
  wm.setConnectTimeout(20);
  wm.setConnectRetries(3);
  wm.setConfigPortalTimeout(PORTAL_TIMEOUT_S);
  wm.setAPCallback([](WiFiManager* m) {
    Serial.print("Setup page open: connect to the WiFi ");
    Serial.print(m->getConfigPortalSSID());
    Serial.println(" and go to http://192.168.4.1");
    digitalWrite(LED_PIN, HIGH);
  });

  String ap = "pingzap-" + String((uint32_t)ESP.getEfuseMac(), HEX).substring(0, 4);
  bool ok = onDemand ? wm.startConfigPortal(ap.c_str(), PORTAL_PASSWORD)
                     : wm.autoConnect(ap.c_str(), PORTAL_PASSWORD);
  digitalWrite(LED_PIN, LOW);

  Settings next = {pServer.getValue(), pUser.getValue(), pPass.getValue(), pApi.getValue(), pToken.getValue()};
  next.apiUrl.trim();
  while (next.apiUrl.endsWith("/")) {
    next.apiUrl.remove(next.apiUrl.length() - 1);
  }
  if (next.mqttServer != cfg.mqttServer || next.mqttUser != cfg.mqttUser || next.mqttPass != cfg.mqttPass ||
      next.apiUrl != cfg.apiUrl || next.deviceToken != cfg.deviceToken) {
    cfg = next;
    saveSettings();
    Serial.println("Settings saved");
  }
  return ok;
}

// Opens the setup page when BOOT is held for PORTAL_HOLD_MS. Outputs are
// switched off first, and the board restarts afterwards to use the new
// settings.
void checkPortalButton() {
  if (digitalRead(BOOT_PIN) != LOW) {
    bootPressedAt = 0;
    return;
  }
  if (bootPressedAt == 0) {
    bootPressedAt = millis();
    return;
  }
  if (millis() - bootPressedAt < PORTAL_HOLD_MS) {
    return;
  }
  Serial.println("BOOT held: opening the setup page");
  alertActive = false;
  setBuzzer(false);
  digitalWrite(GATE_PIN, LOW);
  digitalWrite(RELAY_PIN, LOW);
  relayPulseActive = false;
  client.disconnect();
  runPortal(true);
  ESP.restart();
}

// delay() that still watches the BOOT button.
void waitWatchingButton(unsigned long ms) {
  unsigned long start = millis();
  while (millis() - start < ms) {
    checkPortalButton();
    delay(50);
  }
}

void setup_wifi() {
  Serial.println();
  Serial.println("Connecting to WiFi");
  WiFi.mode(WIFI_STA);
  if (!runPortal(false)) {
    // No network and nobody used the setup page: try again from the start.
    Serial.println("WiFi not configured, restarting");
    ESP.restart();
  }
  Serial.print("WiFi connected: ");
  Serial.print(WiFi.SSID());
  Serial.print(", IP ");
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
    waitWatchingButton(500); // BOOT opens the setup page, e.g. to change networks
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
  http.begin(cfg.apiUrl + "/api/device/config");
  http.addHeader("Authorization", "Bearer " + cfg.deviceToken);
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
    if (client.connect(device_id.c_str(), cfg.mqttUser.c_str(), cfg.mqttPass.c_str(), topic_status.c_str(), 1, true,
                       "offline")) {
      Serial.println("connected");
      client.publish(topic_status.c_str(), "online", true);
      client.subscribe(topic_command.c_str());
    } else {
      Serial.print("failed, rc=");
      Serial.print(client.state());
      Serial.println(" try again in 5 seconds");
      waitWatchingButton(5000); // Nothing to do without the broker
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
  pinMode(BOOT_PIN, INPUT_PULLUP);

  loadSettings();
  setup_wifi();

  // Without its topics the board has nothing to do: retry until pingzap
  // answers (BOOT opens the setup page to fix the API address or token).
  bool changed;
  while (!fetchConfig(&changed)) {
    waitWatchingButton(5000);
  }
  lastConfigFetch = millis();

  client.setServer(cfg.mqttServer.c_str(), mqtt_port);
  client.setCallback(callback);
}

void loop() {
  checkPortalButton();
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
