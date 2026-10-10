# Configuração do broker

[English](broker.md) | **Português (Brasil)**

O pingzap e o ESP32 só precisam alcançar o mesmo broker MQTT e usar os mesmos
tópicos. Este guia cobre as duas formas de fazer isso e como inspecionar o
tráfego com o [MQTTX](https://mqttx.app), um cliente MQTT para desktop.

- [Opção 1 — broker público](#opção-1--broker-público-padrão): sem
  configuração, bom para experimentar o projeto.
- [Opção 2 — deployment próprio no EMQX Cloud](#opção-2--deployment-próprio-no-emqx-cloud):
  privado, com usuário e senha.
- [Testando com o MQTTX](#testando-com-o-mqttx): acompanhe os tópicos e envie
  `ON` / `OFF` manualmente.

Os passos do EMQX Cloud e do MQTTX seguem o guia oficial,
[Connect to Deployments with MQTTX](https://docs.emqx.com/en/cloud/latest/connect_to_deployments/mqttx.html)
(em inglês), que também traz capturas de cada tela.

## Opção 1 — broker público (padrão)

Não há nada a configurar: o pingzap e o firmware já apontam para
`broker.emqx.io`, sem usuário nem senha.

| Lado    | Protocolo                | Endereço                         |
|---------|--------------------------|----------------------------------|
| pingzap | WebSocket sobre TLS/SSL  | `wss://broker.emqx.io:8084/mqtt` |
| ESP32   | MQTT                     | `broker.emqx.io`, porta `1883`   |

Por ser público, qualquer pessoa que conheça os nomes dos tópicos pode lê-los
e publicar `ON` ou `OFF`. Se continuar nele, ao menos troque os tópicos por
algo difícil de adivinhar (veja [Alterando os tópicos](#alterando-os-tópicos)).

## Opção 2 — deployment próprio no EMQX Cloud

### 1. Crie o deployment

1. Cadastre-se no [EMQX Cloud](https://www.emqx.com/en/cloud) e crie um
   deployment. O plano **Serverless** é suficiente para este projeto.
2. Aguarde até o status do deployment ficar **running**.

### 2. Anote o endereço e as portas

Abra a página **Overview** do deployment e anote o endereço do broker e as
portas listadas ali. Elas dependem do plano:

| Plano          | MQTT   | WebSocket | MQTT sobre TLS/SSL | WebSocket sobre TLS/SSL |
|----------------|--------|-----------|--------------------|-------------------------|
| Serverless     | —      | —         | `8883`             | veja a **Overview**     |
| Dedicated Flex | `1883` | `8083`    | `8883` ¹           | `8084` ¹                |
| BYOC           | `1883` | `8083`    | `8883`             | `8884`                  |

¹ Disponível depois de habilitar TLS/SSL nas configurações do deployment.

O Serverless aceita **somente conexões TLS**. O certificado da CA pode ser
baixado na página **Overview**, caso algum cliente o exija.

### 3. Crie as credenciais

Todo deployment do EMQX Cloud tem autenticação habilitada, então os clientes
precisam de usuário e senha.

1. No deployment, acesse **Access Control** → **Authentication**.
2. Adicione um usuário e uma senha. Um par compartilhado entre o pingzap e o
   ESP32 funciona; um par para cada um é melhor, pois assim é possível revogar
   um sem afetar o outro.

### 4. Configure o pingzap

No `.env`, com o endereço, a porta e as credenciais dos passos anteriores:

```sh
# MQTT sobre TLS/SSL
PINGZAP_MQTT_BROKER=ssl://xxxxx.emqxsl.com:8883
PINGZAP_MQTT_USERNAME=pingzap
PINGZAP_MQTT_PASSWORD=sua-senha
```

O esquema da URL define o protocolo:

| Esquema  | Protocolo               | Exemplo                              |
|----------|-------------------------|--------------------------------------|
| `tcp://` | MQTT                    | `tcp://broker.example.com:1883`      |
| `ssl://` | MQTT sobre TLS/SSL      | `ssl://broker.example.com:8883`      |
| `ws://`  | WebSocket               | `ws://broker.example.com:8083/mqtt`  |
| `wss://` | WebSocket sobre TLS/SSL | `wss://broker.example.com:8084/mqtt` |

O pingzap valida o certificado do broker com as CAs confiáveis do sistema, o
que normalmente basta para o EMQX Cloud; não é preciso configurar um arquivo
de CA.

### 5. Configure o ESP32

Em `firmware/pingzap/secrets.h`, preencha o login:

```cpp
const char* mqtt_user = "esp32";
const char* mqtt_pass = "sua-senha";
```

Em `firmware/pingzap/pingzap.ino`, ajuste o endereço e a porta:

```cpp
const char* mqtt_server = "xxxxx.emqxsl.com";
const int mqtt_port = 1883;
```

O sketch, como está, fala **MQTT puro** (`WiFiClient`), que funciona na porta
`1883` de deployments Dedicated Flex e BYOC. Em um deployment somente TLS, como
o Serverless, ele também precisa passar a usar TLS:

```cpp
#include <WiFiClientSecure.h>

WiFiClientSecure espClient;       // no lugar de: WiFiClient espClient;

// no setup(), antes de client.setServer(...):
espClient.setCACert(ca_cert);     // certificado da CA baixado na Overview
```

com `mqtt_port` em `8883` e `ca_cert` contendo o conteúdo do certificado
baixado. `espClient.setInsecure()` pula a verificação do certificado e só é
aceitável para um teste rápido.

## Testando com o MQTTX

O MQTTX permite ver o que passa pelo broker e fazer o papel de qualquer um dos
lados: publicar `ON` sem enviar mensagem no WhatsApp, ou acompanhar o que o
pingzap publica sem ter a placa.

### 1. Crie a conexão

Instale o [MQTTX](https://mqttx.app), clique em **New Connection** e preencha:

| Campo           | Broker público                      | Deployment no EMQX Cloud                 |
|-----------------|-------------------------------------|------------------------------------------|
| **Name**        | qualquer nome, ex.: `pingzap`       | qualquer nome, ex.: `pingzap`            |
| **Client ID**   | mantenha o gerado                   | mantenha o gerado                        |
| **Host**        | `mqtt://` + `broker.emqx.io`        | protocolo da sua porta + seu endereço    |
| **Port**        | `1883`                              | porta da página **Overview**             |
| **Username**    | vazio                               | o criado em **Authentication**           |
| **Password**    | vazio                               | a criada em **Authentication**           |
| **SSL/TLS**     | desligado                           | ligado para `mqtts://` e `wss://`        |

O protocolo selecionado ao lado de **Host** precisa corresponder à porta:
`mqtt://` para MQTT, `ws://` para WebSocket, `mqtts://` para MQTT sobre TLS/SSL
e `wss://` para WebSocket sobre TLS/SSL.

O **Client ID** precisa ser único no broker. Não reutilize o do ESP32
(`ESP32_Pro_Unit_01`): o broker desconectaria a placa. O ícone de atualizar ao
lado do campo gera um novo.

Clique em **Connect**, no canto superior direito.

### 2. Assine os tópicos

Clique em **New Subscription**, informe `esp32/unit01/#` e confirme. O curinga
`#` cobre os três tópicos usados pelo projeto:

| Tópico                | O que aparece |
|-----------------------|---------------|
| `esp32/unit01/cmd`    | `ON` / `OFF` publicados pelo pingzap. |
| `esp32/unit01/data`   | Telemetria do ESP32 a cada 5 s e o estado do LED. |
| `esp32/unit01/status` | `online` / `offline` do ESP32. |

### 3. Publique um comando

Na barra de mensagem, na parte de baixo, defina o tópico como
`esp32/unit01/cmd`, o formato como **Plaintext**, digite `ON` como payload e
envie. O LED começa a piscar; envie `OFF` para parar.

O payload precisa ser exatamente `ON` ou `OFF`, em maiúsculas e sem aspas ou
quebras de linha.

Para mais detalhes sobre assinar e publicar, veja o
[guia do MQTTX](https://www.emqx.com/en/blog/mqtt-x-guideline) (em inglês).

## Alterando os tópicos

Os nomes dos tópicos ficam em dois lugares e precisam ser iguais nos dois:

- pingzap: `PINGZAP_MQTT_CMD_TOPIC` e `PINGZAP_MQTT_DATA_TOPIC` no `.env`.
- ESP32: `topic_command`, `topic_telemetry` e `topic_status` em
  `firmware/pingzap/pingzap.ino`.

Se mais de uma placa usar o mesmo broker, dê também um `device_id` diferente a
cada uma no sketch.

## Solução de problemas

| Sintoma | Causa provável |
|---------|----------------|
| O pingzap inicia, mas nunca registra `connected to ...` | Ele fica tentando de novo em silêncio: endereço, porta ou esquema errados, deployment fora de **running**, ou usuário/senha que não conferem com **Authentication**. Teste os mesmos valores no MQTTX, que mostra o erro. |
| O serial do ESP32 mostra `failed, rc=-2` | A placa não alcança o endereço/porta; em deployments somente TLS, o sketch ainda está em MQTT puro. |
| O serial do ESP32 mostra `failed, rc=4` ou `rc=5` | `mqtt_user` / `mqtt_pass` errados. |
| O ESP32 reconecta a cada poucos segundos | Outro cliente está usando o mesmo `device_id`. |
| O pingzap publica, mas o LED não reage | Tópicos diferentes entre o `.env` e o sketch, ou os dois lados estão em brokers diferentes. |
