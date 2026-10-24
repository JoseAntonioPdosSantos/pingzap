#!/bin/sh
# Gera o arquivo de senhas do Mosquitto a partir das variáveis do .env e
# inicia o broker. Rodar a cada início mantém o arquivo em sincronia com o .env.
set -eu

passwd_file=/mosquitto/passwd

# The file survives container restarts; -c refuses to overwrite it.
rm -f "$passwd_file"
mosquitto_passwd -c -b "$passwd_file" "$PINGZAP_MQTT_USERNAME" "$PINGZAP_MQTT_PASSWORD"
mosquitto_passwd -b "$passwd_file" "$ESP32_MQTT_USERNAME" "$ESP32_MQTT_PASSWORD"
chown mosquitto:mosquitto "$passwd_file"
chmod 0600 "$passwd_file"

exec /docker-entrypoint.sh mosquitto -c /mosquitto/config/mosquitto.conf
