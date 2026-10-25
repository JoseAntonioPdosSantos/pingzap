# pingzap

[English](README.md) | **Português (Brasil)**

Ponte entre o WhatsApp e placas ESP32 via MQTT, gerenciada por uma interface
web. Contatos e grupos do WhatsApp conversam com **bots**, e os bots enviam
comandos às placas:

- **Bot de fluxo**: um chatbot com menus. Exemplo do portão: alguém envia
  `/start`, o bot responde "Digite 1 para abrir o portão"; ao receber `1`, o
  pingzap publica `PULSE` na fila do ESP32, que pulsa o relé, e o bot pergunta
  se o portão abriu.
- **Bot de alerta**: quando um contato configurado envia mensagem (com uma
  palavra-chave opcional), o ESP32 pisca o LED e toca o buzzer; quando eu
  respondo, o alerta para.

Pela interface web se faz tudo: parear o WhatsApp por QR code, cadastrar as
placas e os nomes das filas (tópicos MQTT), criar os bots e escolher qual
contato ou grupo usa cada um. As placas buscam na API do pingzap as filas que
devem usar.

## Fluxo do bot de portão

```
 pessoa (WhatsApp)              pingzap                       ESP32
    │── /start ────────────────────▶│                             │
    │◀─ "O portão está fechado.     │                             │
    │    *1* - Abrir o portão"      │                             │
    │── 1 ─────────────────────────▶│── PULSE em <fila cmd> ─────▶│ pulso no relé
    │◀─ "O portão abriu?            │                             │
    │    *1* - Sim  *2* - Não"      │                             │
    │── 2 ─────────────────────────▶│── PULSE (nova tentativa) ──▶│ pulso no relé
    │◀─ "O portão abriu?" ...       │                             │
    │   (sem resposta por 3 min)    │                             │
    │◀─ "Ainda preciso da sua       │                             │
    │    resposta: O portão abriu?" │                             │
    │── 1 ─────────────────────────▶│ estado = aberto             │
    │◀─ "Portão aberto."            │                             │
    │── /menu ─────────────────────▶│                             │
    │◀─ "O portão está aberto.      │                             │
    │    *1* - Fechar o portão      │                             │
    │    *2* - Informar que o portão│                             │
    │          foi fechado pelo     │                             │
    │          controle"            │                             │
```

### Regras dos bots de fluxo

- O bot é uma **máquina de estados**. O estado atual (ex.: fechado/aberto) é
  único por bot e compartilhado por todos os contatos e grupos dele.
- Cada estado tem um menu numerado. Cada opção pode:
  - publicar um **comando** no tópico de comandos do dispositivo do bot;
  - fazer uma **pergunta de confirmação** (`1` Sim / `2` Não). Com `2`, o
    comando é publicado de novo e a pergunta se repete, até o limite de
    **tentativas** do fluxo (0 = sem limite);
  - **mudar o estado** quando a confirmação é `1`, ou de imediato se não houver
    confirmação.
- Uma opção pode ter um **atalho** (ex.: `/abrir`, `/fechar`) que a executa
  direto, sem passar pelo menu, seguindo a mesma confirmação, tentativas e
  lembretes. O atalho só vale no estado em que a opção existe: `/abrir` com o
  portão já aberto responde com o menu atual. O menu mostra o atalho ao lado de
  cada opção.
- Uma opção sem comando só muda o estado. É assim que se informa que o portão
  foi aberto ou fechado por fora (pelo controle). O estado também pode ser
  ajustado na página **Bots**.
- Sem resposta à confirmação, o bot **insiste** a cada N minutos (3 por padrão)
  até a pessoa responder.
- Se ninguém responder depois de N lembretes (3 por padrão, ou seja, cerca de
  9 minutos), o bot **aciona o alarme** do dispositivo, publicando `ON` (LED
  piscando e buzzer), e avisa no chat para alguém verificar o que está
  acontecendo. Os lembretes continuam. A primeira resposta no chat desliga o
  alarme, publicando `OFF`, e segue o fluxo normalmente. O número de lembretes
  e os comandos são configuráveis; 0 desativa o alarme.
- Fora de uma conversa, o bot só reage ao comando de início (`/start`) ou de
  menu (`/menu`). As demais mensagens do chat são ignoradas.
- Se outra pessoa mudar o estado enquanto alguém está com o menu aberto, o bot
  mostra o menu atualizado em vez de executar a opção antiga.
- Mensagens enviadas pela própria conta pareada não são tratadas como
  respostas, porque o bot escreve a partir dela.
- Salvar um bot reinicia as conversas em andamento dele.

### Regras dos bots de alerta

- Uma mensagem de um contato ou grupo do bot publica o comando de ligar (`ON`
  por padrão). Com **palavra-chave** (ex.: `/urgente`), só as mensagens que a
  contêm disparam: a comparação ignora maiúsculas e minúsculas e vale para o
  texto e para legendas de mídia.
- Quando eu respondo, de qualquer aparelho, aquele chat deixa de aguardar
  resposta. Quando ninguém mais aguarda, o pingzap publica o comando de
  desligar (`OFF`).
- Os alertas pendentes ficam só em memória: depois de reiniciar o pingzap, a
  próxima resposta já desliga o alerta.

### Contatos e grupos

- Cada contato ou grupo conversa com **um único bot**.
- Contatos são informados como DDD + telefone, e o `55` é adicionado. O número
  também pode ser escolhido na lista de contatos do WhatsApp. Contas antigas
  sem o nono dígito são reconhecidas.
- Em grupos, qualquer participante pode usar o bot.

## Arquitetura

```
pingzap/
├── cmd/pingzap/main.go     # liga as partes e sobe o servidor web
├── internal/
│   ├── config/             # .env: broker, bancos, endereço HTTP
│   ├── store/              # SQLite: usuários, sessões, dispositivos, bots, conversas
│   ├── flow/               # motor de fluxo (máquina de estados), sem I/O
│   ├── bot/                # roteia mensagens ao bot certo, alertas, lembretes, telemetria
│   ├── alert/              # quais chats ainda aguardam resposta (bots de alerta)
│   ├── phone/              # normalização de números brasileiros
│   ├── whatsapp/           # sessão, QR code, contatos, grupos, envio
│   ├── mqtt/               # conexão com o broker, publicação, assinaturas
│   ├── metrics/            # grava a telemetria no InfluxDB (para o Grafana)
│   └── web/                # API REST + interface embutida (dist/)
├── web/                    # interface React (Vite)
├── firmware/pingzap/       # sketch do ESP32
├── mosquitto/              # broker local do docker-compose
├── grafana/                # datasource e dashboard do Grafana (provisionados)
└── docker-compose.yml
```

São dois bancos SQLite: `pingzap.db`, com os dados do pingzap, e
`whatsapp.db`, com a sessão do WhatsApp.

## Como executar

### Com Docker (recomendado)

```sh
cp .env_example .env    # ajuste as senhas do broker
docker compose up -d --build
```

Abra `http://localhost:8080`:

1. **Primeiro acesso**: crie o usuário administrador. Outros usuários podem ser
   criados em **Usuários**.
2. **WhatsApp**: clique em **Conectar** e leia o QR code no celular, em
   *Aparelhos conectados > Conectar um aparelho*.
3. **Dispositivos**: cadastre o ESP32, com nome, client ID e os três tópicos (o
   botão **Sugerir** preenche os tópicos), e copie o **token**.
4. **Bots**: crie um bot. **Novo bot de portão** já vem com o fluxo pronto.
   Escolha o dispositivo e os contatos ou grupos e salve.

O compose sobe também o broker Mosquitto (veja o
[guia do broker](docs/broker.pt-BR.md)), o InfluxDB e o Grafana. Os bancos do
pingzap ficam em `./data`.

### Gráficos (Grafana)

A telemetria de todas as placas é gravada pelo pingzap no InfluxDB (bucket
`pingzap`, guardada por 90 dias). O Grafana fica em `http://localhost:3000`
(usuário `admin`, senha `GRAFANA_PASSWORD` do `.env`) e já abre no dashboard
**pingzap — Dispositivos**, com o datasource configurado:

- status online/offline e conexão com o broker ao longo do tempo;
- alerta (LED e buzzer) ligado/desligado;
- pulsos do relé e alertas disparados no período;
- sinal do Wi-Fi, memória livre e tempo ligado;
- últimos comandos recebidos.

O seletor **Dispositivo** filtra por placa (pelo nome cadastrado na web). O
dashboard é provisionado a partir de `grafana/dashboards/pingzap.json`; para
alterá-lo de vez, edite esse arquivo, porque as mudanças feitas só na tela se
perdem quando o container é recriado.

### Sem Docker

Requisitos: Go, um compilador C (o driver SQLite usa cgo) e Node 18+ para a
interface.

```sh
cd web && npm install && npm run build && cd ..   # gera internal/web/dist
go run ./cmd/pingzap
```

Para desenvolver a interface com recarga automática, rode o pingzap e, em
outro terminal, `cd web && npm run dev`. O Vite encaminha `/api` para a porta
8080.

Testes:

```sh
go test ./...
```

## Configuração (.env)

Só a infraestrutura fica no `.env`. Todo o resto é configurado pela web.

| Variável                | Padrão                               | Descrição |
|-------------------------|--------------------------------------|-----------|
| `PINGZAP_MQTT_BROKER`   | `wss://broker.emqx.io:8084/mqtt`     | URL do broker (no compose: `tcp://mosquitto:1883`). |
| `PINGZAP_MQTT_USERNAME` | vazio                                | Usuário do pingzap no broker. |
| `PINGZAP_MQTT_PASSWORD` | vazio                                | Senha do pingzap no broker. |
| `ESP32_MQTT_USERNAME`   | —                                    | Usuário do ESP32, criado no broker local pelo compose. |
| `ESP32_MQTT_PASSWORD`   | —                                    | Senha do ESP32 no broker local. |
| `PINGZAP_HTTP_ADDR`     | `:8080`                              | Endereço da interface web e da API. |
| `PINGZAP_DB`            | `file:pingzap.db?_foreign_keys=on`   | Banco com usuários, dispositivos e bots. |
| `PINGZAP_WHATSAPP_DB`   | `file:whatsapp.db?_foreign_keys=on`  | Banco com a sessão do WhatsApp. |
| `INFLUXDB_PASSWORD`     | —                                    | Senha do usuário `admin` do InfluxDB (compose). |
| `INFLUXDB_TOKEN`        | —                                    | Token do InfluxDB, usado pelo pingzap e pelo Grafana (compose). |
| `GRAFANA_PASSWORD`      | —                                    | Senha do usuário `admin` do Grafana (compose). |
| `PINGZAP_INFLUX_URL`    | vazio                                | InfluxDB que recebe a telemetria; vazio desliga (no compose: `http://influxdb:8086`). |
| `PINGZAP_INFLUX_TOKEN`  | vazio                                | Token do InfluxDB (no compose: `INFLUXDB_TOKEN`). |
| `PINGZAP_INFLUX_ORG`    | `pingzap`                            | Organização do InfluxDB. |
| `PINGZAP_INFLUX_BUCKET` | `pingzap`                            | Bucket do InfluxDB. |

**Migração da versão anterior:** se o `.env` ainda tiver `PINGZAP_NUMBERS`
(com `PINGZAP_KEYWORD` e os tópicos/payloads antigos) e o banco estiver vazio,
o pingzap cria a partir deles um dispositivo e um bot de alerta, uma única vez.
Depois disso essas variáveis são ignoradas.

## ESP32

O sketch fica em `firmware/pingzap/pingzap.ino` e precisa das bibliotecas
`PubSubClient` e `ArduinoJson` (v7).

```sh
cp firmware/pingzap/secrets_example.h firmware/pingzap/secrets.h
```

No `secrets.h`, preencha:

- o Wi-Fi (`ssid`, `password`);
- o broker (`mqtt_server`, `mqtt_user`, `mqtt_pass`);
- a API do pingzap: `api_url`, por exemplo `http://192.168.0.10:8080`, e o
  `device_token` copiado da página **Dispositivos**.

Ao ligar, a placa pede à API o seu client ID e os seus tópicos, e só então se
conecta ao broker. A cada minuto ela confere a configuração; se os tópicos
mudaram na web, reconecta usando os novos.

### Comandos

| Payload | Efeito |
|---------|--------|
| `ON`    | Inicia o alerta: LED piscando e buzzer. |
| `OFF`   | Encerra o alerta. |
| `PULSE` | Aciona o portão: `GATE_PIN` (D5) e o relé (`RELAY_PIN`, D23) ficam ligados juntos por 1 s (`RELAY_PULSE_MS`). |

### Telemetria

A placa publica no tópico de telemetria a cada 5 s e a cada comando recebido,
sempre com os mesmos campos:

```json
{"device": "esp-portao", "uptime_s": 3600, "wifi_rssi": -61, "free_heap": 210452,
 "alert": false, "relay": true, "event": "PULSE"}
```

| Campo       | Significado |
|-------------|-------------|
| `uptime_s`  | Segundos desde que a placa ligou. |
| `wifi_rssi` | Sinal do Wi-Fi, em dBm. |
| `free_heap` | Memória livre, em bytes. |
| `alert`     | Alerta (LED e buzzer) ligado. |
| `relay`     | Pulso do portão (D5 e D23) em andamento. |
| `event`     | Comando que gerou a mensagem (`ON`, `OFF`, `PULSE`); ausente no envio periódico. |

O status `online` / `offline` vai no tópico de status (LWT, retido). A página
**Dispositivos** mostra a última mensagem e o status, e o Grafana guarda o
histórico.

Ao conectar ao Wi-Fi, no boot ou depois de uma queda, o LED pisca 3 vezes.

### Buzzer

Ligue o terminal positivo no `D4` (GPIO 4) e o outro no `GND`. O sketch aciona
um buzzer passivo com um tom de 2 kHz (`BUZZER_FREQ`).

### Simulador

Há uma [simulação no Wokwi](https://wokwi.com/projects/477507248547038209)
de uma versão anterior do firmware. Ela usa o broker público e tópicos fixos.
Por rodar na nuvem, não alcança o broker nem a API da sua rede local.

## Observações

- A interface web e a API usam HTTP puro, sem TLS. Exponha-as só na rede local
  ou atrás de um proxy reverso com HTTPS.
- Quem tem acesso à interface pode abrir o portão e parear o WhatsApp; crie
  usuários só para quem precisa.
- O token de cada dispositivo dá acesso à configuração daquela placa. Se ele
  vazar, gere um novo em **Dispositivos**.
- `whatsapp.db` dá acesso à conta do WhatsApp, `pingzap.db` contém os dados do
  sistema, o `.env` tem as senhas do broker e o `secrets.h` as do Wi-Fi. Todos
  estão no `.gitignore` e não devem ser versionados.
- Os comandos não são retidos no broker: se o ESP32 estiver desligado quando o
  comando for publicado, ele não o recebe. Nos bots de fluxo, a pergunta de
  confirmação deixa isso visível para quem pediu.
