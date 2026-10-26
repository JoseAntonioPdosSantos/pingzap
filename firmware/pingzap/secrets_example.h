// Copy to secrets.h. Everything here is only a default: the WiFi and these
// values are set on the board's setup page (network "pingzap-XXXX") and kept
// in flash. Filling them in saves typing on the page.

// Password of the setup network "pingzap-XXXX" (at least 8 characters).
#define PORTAL_PASSWORD "troque-esta-senha"

// MQTT broker. For the local broker from docker-compose, use the LAN IP of
// the machine running it; for the public broker, "broker.emqx.io".
const char* mqtt_server = "192.168.0.10";

// MQTT login: ESP32_MQTT_USERNAME / ESP32_MQTT_PASSWORD from .env. Leave blank
// for public brokers.
const char* mqtt_user = "esp32";
const char* mqtt_pass = "";

// pingzap API: the board fetches its MQTT client ID and topics from it. Use
// the LAN IP of the machine running pingzap and the token shown for this
// board on the Dispositivos page.
const char* api_url = "http://192.168.0.10:8080";
const char* device_token = "COLE_O_TOKEN_DO_DISPOSITIVO";
