# Broker setup

**English** | [Português (Brasil)](broker.pt-BR.md)

pingzap and the ESP32 only need to reach the same MQTT broker and agree on the
topics. This guide covers the two ways of doing that and how to inspect the
traffic with [MQTTX](https://mqttx.app), a desktop MQTT client.

- [Option 1 — public broker](#option-1--public-broker-default): no setup,
  good for trying the project out.
- [Option 2 — your own EMQX Cloud deployment](#option-2--your-own-emqx-cloud-deployment):
  private, with username and password.
- [Testing with MQTTX](#testing-with-mqttx): watch the topics and send `ON` /
  `OFF` by hand.

The steps for EMQX Cloud and MQTTX follow the official guide,
[Connect to Deployments with MQTTX](https://docs.emqx.com/en/cloud/latest/connect_to_deployments/mqttx.html),
which also has screenshots of each screen.

## Option 1 — public broker (default)

Nothing to configure: pingzap and the firmware already point to
`broker.emqx.io`, with no username or password.

| Side    | Protocol                | Address                          |
|---------|-------------------------|----------------------------------|
| pingzap | WebSocket over TLS/SSL  | `wss://broker.emqx.io:8084/mqtt` |
| ESP32   | MQTT                    | `broker.emqx.io`, port `1883`    |

Being public, anyone who knows the topic names can read them and publish `ON`
or `OFF`. If you stay on it, at least change the topics to something hard to
guess (see [Changing the topics](#changing-the-topics)).

## Option 2 — your own EMQX Cloud deployment

### 1. Create the deployment

1. Sign up at [EMQX Cloud](https://www.emqx.com/en/cloud) and create a
   deployment. The **Serverless** plan is enough for this project.
2. Wait until the deployment status is **running**.

### 2. Get the address and ports

Open the deployment **Overview** page and note the broker address and the
ports listed there. They depend on the plan:

| Plan           | MQTT   | WebSocket | MQTT over TLS/SSL | WebSocket over TLS/SSL |
|----------------|--------|-----------|-------------------|------------------------|
| Serverless     | —      | —         | `8883`            | see **Overview**       |
| Dedicated Flex | `1883` | `8083`    | `8883` ¹          | `8084` ¹               |
| BYOC           | `1883` | `8083`    | `8883`            | `8884`                 |

¹ Available after enabling TLS/SSL in the deployment settings.

Serverless accepts **TLS connections only**. Its CA certificate can be
downloaded from the **Overview** page, in case a client asks for one.

### 3. Create the credentials

Every EMQX Cloud deployment has authentication enabled, so clients need a
username and password.

1. In the deployment, go to **Access Control** → **Authentication**.
2. Add a username and password. One pair shared by pingzap and the ESP32 works;
   one pair for each is better, since either can then be revoked on its own.

### 4. Configure pingzap

In `.env`, using the address, port and credentials from the previous steps:

```sh
# MQTT over TLS/SSL
PINGZAP_MQTT_BROKER=ssl://xxxxx.emqxsl.com:8883
PINGZAP_MQTT_USERNAME=pingzap
PINGZAP_MQTT_PASSWORD=your-password
```

The scheme in the URL selects the protocol:

| Scheme   | Protocol               | Example                             |
|----------|------------------------|-------------------------------------|
| `tcp://` | MQTT                   | `tcp://broker.example.com:1883`     |
| `ssl://` | MQTT over TLS/SSL      | `ssl://broker.example.com:8883`     |
| `ws://`  | WebSocket              | `ws://broker.example.com:8083/mqtt` |
| `wss://` | WebSocket over TLS/SSL | `wss://broker.example.com:8084/mqtt`|

pingzap validates the broker certificate against the system's trusted CAs,
which is normally enough for EMQX Cloud; no CA file has to be configured.

### 5. Configure the ESP32

In `firmware/pingzap/secrets.h`, fill in the login:

```cpp
const char* mqtt_user = "esp32";
const char* mqtt_pass = "your-password";
```

In `firmware/pingzap/pingzap.ino`, set the address and port:

```cpp
const char* mqtt_server = "xxxxx.emqxsl.com";
const int mqtt_port = 1883;
```

The sketch as shipped speaks **plain MQTT** (`WiFiClient`), which works on port
`1883` of Dedicated Flex and BYOC deployments. On a TLS-only deployment such as
Serverless it also needs to switch to TLS:

```cpp
#include <WiFiClientSecure.h>

WiFiClientSecure espClient;       // instead of: WiFiClient espClient;

// in setup(), before client.setServer(...):
espClient.setCACert(ca_cert);     // CA certificate downloaded from Overview
```

with `mqtt_port` set to `8883` and `ca_cert` holding the contents of the
downloaded certificate. `espClient.setInsecure()` skips the certificate check
and is acceptable only for a quick test.

## Testing with MQTTX

MQTTX lets you see what goes through the broker and stand in for either side:
publish `ON` without sending a WhatsApp message, or watch what pingzap
publishes without having the board.

### 1. Create the connection

Install [MQTTX](https://mqttx.app), click **New Connection** and fill in:

| Field           | Public broker                       | EMQX Cloud deployment                 |
|-----------------|-------------------------------------|---------------------------------------|
| **Name**        | anything, e.g. `pingzap`            | anything, e.g. `pingzap`              |
| **Client ID**   | keep the generated one              | keep the generated one                |
| **Host**        | `mqtt://` + `broker.emqx.io`        | protocol for your port + your address |
| **Port**        | `1883`                              | port from the **Overview** page       |
| **Username**    | empty                               | the one created in **Authentication** |
| **Password**    | empty                               | the one created in **Authentication** |
| **SSL/TLS**     | off                                 | on for `mqtts://` and `wss://`        |

The protocol selected next to **Host** must match the port: `mqtt://` for
MQTT, `ws://` for WebSocket, `mqtts://` for MQTT over TLS/SSL and `wss://` for
WebSocket over TLS/SSL.

The **Client ID** must be unique on the broker. Do not reuse the ESP32's
(`ESP32_Pro_Unit_01`): the broker would disconnect the board. The refresh icon
next to the field generates a new one.

Click **Connect** in the upper-right corner.

### 2. Subscribe to the topics

Click **New Subscription**, enter `esp32/unit01/#` and confirm. The `#`
wildcard covers the three topics used by the project:

| Topic                 | What shows up |
|-----------------------|---------------|
| `esp32/unit01/cmd`    | `ON` / `OFF` published by pingzap. |
| `esp32/unit01/data`   | Telemetry from the ESP32 every 5 s and the LED state. |
| `esp32/unit01/status` | `online` / `offline` for the ESP32. |

### 3. Publish a command

In the message bar at the bottom, set the topic to `esp32/unit01/cmd`, the
format to **Plaintext**, type `ON` as the payload and send. The LED starts
blinking; send `OFF` to stop it.

The payload must be exactly `ON` or `OFF`, uppercase and without quotes or
line breaks.

For more on subscribing and publishing, see the
[MQTTX guideline](https://www.emqx.com/en/blog/mqtt-x-guideline).

## Changing the topics

The topic names live in two places and must be the same in both:

- pingzap: `PINGZAP_MQTT_CMD_TOPIC` and `PINGZAP_MQTT_DATA_TOPIC` in `.env`.
- ESP32: `topic_command`, `topic_telemetry` and `topic_status` in
  `firmware/pingzap/pingzap.ino`.

If more than one board shares the broker, also give each a different
`device_id` in the sketch.

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| pingzap starts but never logs `connected to ...` | It keeps retrying silently: wrong address, port or scheme, the deployment is not **running**, or the username/password does not match **Authentication**. Try the same values in MQTTX, which shows the error. |
| ESP32 serial shows `failed, rc=-2` | The board cannot reach the address/port; on TLS-only deployments, the sketch is still using plain MQTT. |
| ESP32 serial shows `failed, rc=4` or `rc=5` | Wrong `mqtt_user` / `mqtt_pass`. |
| ESP32 keeps reconnecting every few seconds | Another client is using the same `device_id`. |
| pingzap publishes but the LED does not react | Topics differ between `.env` and the sketch, or the two sides are on different brokers. |
