/*
 * PROFESSIONAL MQTT EXPERIMENT - ESP32
 * * Features:
 * - Non-blocking Architecture (No delay())
 * - Automatic Reconnection (WiFi & MQTT)
 * - LWT (Last Will & Testament) for State Monitoring
 * - JSON Data Serialization
 * - Remote Command Handling
 */

#include <WiFi.h>
#include <PubSubClient.h>
#include <ArduinoJson.h>

// ==========================================
// 1. CONFIGURATION (Edit these)
// ==========================================
// WiFi credentials (ssid / password) and the MQTT login (mqtt_user /
// mqtt_pass) live in secrets.h, which is not versioned. Copy
// secrets_example.h to secrets.h and fill it in.
#include "secrets.h"

// MQTT Broker Settings (Using public HiveMQ for demo, change for production)
const char* mqtt_server = "broker.emqx.io";
const int mqtt_port = 1883; 

// Unique Device ID (Must be unique on the broker)
const char* device_id = "ESP32_Pro_Unit_01"; 

// Topics (Structure: device_type/device_id/function)
const char* topic_telemetry = "esp32/unit01/data";   // Where we send sensor data
const char* topic_command   = "esp32/unit01/cmd";    // Where we listen for commands
const char* topic_status    = "esp32/unit01/status"; // LWT (Online/Offline)

// ==========================================
// 2. GLOBAL OBJECTS & VARIABLES
// ==========================================
WiFiClient espClient;
PubSubClient client(espClient);

// Timers for non-blocking delays
unsigned long lastMsgTime = 0;
const long interval = 5000; // Send data every 5 seconds

#define LED_PIN 2 // Onboard LED
#define BUZZER_PIN 4 // D4 -> buzzer (+); buzzer (-) -> GND

// Passive buzzer: tone frequency in Hz. Set to 0 for an active buzzer, which
// only needs the pin driven HIGH.
const unsigned int BUZZER_FREQ = 2000;

// Alert blink (non-blocking)
bool alertActive = false;
unsigned long alertStart = 0;
unsigned long lastBlink = 0;
const unsigned long ALERT_DURATION = 0;    // ms; 0 = blink until "OFF" arrives
const unsigned long BLINK_INTERVAL = 150;  // fast blink = alert

// Connection signal (non-blocking): blinks when WiFi + broker are up
int connectTogglesLeft = 0;
unsigned long lastConnectBlink = 0;
const int CONNECT_BLINKS = 3;
const unsigned long CONNECT_BLINK_INTERVAL = 150; // ms on, then ms off

// Beeps along with the LED while the alert is active
void setBuzzer(bool on) {
  if (BUZZER_FREQ == 0) {
    digitalWrite(BUZZER_PIN, on ? HIGH : LOW);
  } else if (on) {
    tone(BUZZER_PIN, BUZZER_FREQ);
  } else {
    noTone(BUZZER_PIN);
  }
}

// ==========================================
// 3. SETUP WIFI
// ==========================================
void setup_wifi() {
  delay(10);
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
}

// ==========================================
// 4. CALLBACK (Handle Incoming Messages)
// ==========================================
void callback(char* topic, byte* payload, unsigned int length) {
  Serial.print("Message arrived [");
  Serial.print(topic);
  Serial.print("] ");

  // Convert payload to string for easier handling
  String message;
  for (int i = 0; i < length; i++) {
    message += (char)payload[i];
  }
  Serial.println(message);

  // -- Command Logic --
  // Example: If we receive "ON", turn on LED
  if (String(topic) == topic_command) {
    if (message == "ON") {
      alertActive = true;
      connectTogglesLeft = 0; // the alert takes over the LED
      alertStart = millis();
      lastBlink = alertStart;
      digitalWrite(LED_PIN, HIGH);
      setBuzzer(true);
      // Feedback: Publish new state immediately
      client.publish(topic_telemetry, "{\"led\": \"ON\"}"); 
    } else if (message == "OFF") {
      alertActive = false; // cancel alert
      digitalWrite(LED_PIN, LOW);
      setBuzzer(false);
      client.publish(topic_telemetry, "{\"led\": \"OFF\"}");
    }
  }
}

// ==========================================
// 5. RECONNECT (The Engine Room)
// ==========================================
void reconnect() {
  // Loop until we're reconnected
  while (!client.connected()) {
    Serial.print("Attempting MQTT connection...");
    
    // --- LWT CONFIGURATION ---
    // define Last Will: Topic, QoS, Retain, Message
    // If this ESP32 dies, the Broker will post "offline" to the status topic automatically.
    
    if (client.connect(device_id, mqtt_user, mqtt_pass, topic_status, 1, true, "offline")) {
      Serial.println("connected");
      
      // Once connected, publish an announcement that we are alive (Retained = true)
      client.publish(topic_status, "online", true);
      
      // Resubscribe to command topics
      client.subscribe(topic_command);

      // Signal the successful connection, unless an alert owns the LED
      if (!alertActive) {
        digitalWrite(LED_PIN, HIGH);
        lastConnectBlink = millis();
        connectTogglesLeft = CONNECT_BLINKS * 2 - 1; // ends with the LED off
      }
    } else {
      Serial.print("failed, rc=");
      Serial.print(client.state());
      Serial.println(" try again in 5 seconds");
      delay(5000); // Blocking delay here is acceptable as we can't operate without connection
    }
  }
}

// ==========================================
// 6. MAIN SETUP
// ==========================================
void setup() {
  Serial.begin(115200);
  pinMode(LED_PIN, OUTPUT);
  digitalWrite(LED_PIN, LOW);
  pinMode(BUZZER_PIN, OUTPUT);
  digitalWrite(BUZZER_PIN, LOW);
  
  setup_wifi();
  
  client.setServer(mqtt_server, mqtt_port);
  client.setCallback(callback);
}

// ==========================================
// 7. MAIN LOOP
// ==========================================
void loop() {
  // Ensure we stay connected
  if (!client.connected()) {
    reconnect();
  }
  client.loop(); // Keep MQTT alive
  
  // --- Alert blink handler ---
  if (alertActive) {
    unsigned long t = millis();
    if (ALERT_DURATION > 0 && t - alertStart >= ALERT_DURATION) {
      alertActive = false;
      digitalWrite(LED_PIN, LOW); // timed out without an "OFF"
      setBuzzer(false);
      client.publish(topic_telemetry, "{\"led\": \"OFF\"}");
    } else if (t - lastBlink >= BLINK_INTERVAL) {
      lastBlink = t;
      digitalWrite(LED_PIN, !digitalRead(LED_PIN));
      setBuzzer(digitalRead(LED_PIN));
    }
  } else if (connectTogglesLeft > 0) {
    // --- Connection blink handler ---
    unsigned long t = millis();
    if (t - lastConnectBlink >= CONNECT_BLINK_INTERVAL) {
      lastConnectBlink = t;
      digitalWrite(LED_PIN, !digitalRead(LED_PIN));
      connectTogglesLeft--;
    }
  }

  // --- Non-Blocking Timer for Telemetry ---
  unsigned long now = millis();
  if (now - lastMsgTime > interval) {
    lastMsgTime = now;
    
    // Create a JSON Document
    JsonDocument doc; // ArduinoJson v7
    doc["device"] = device_id;
    doc["uptime"] = millis() / 1000;
    doc["wifi_rssi"] = WiFi.RSSI();
    
    // Add dynamic data (simulated sensor)
    doc["temp"] = random(20, 30); 

    // Serialize JSON to String
    char buffer[256];
    serializeJson(doc, buffer);

    // Publish to MQTT
    Serial.print("Publishing data: ");
    Serial.println(buffer);
    client.publish(topic_telemetry, buffer);
  }
}