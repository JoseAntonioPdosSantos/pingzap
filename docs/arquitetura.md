# Arquitetura do pingzap

Como as peças do pingzap se ligam, do WhatsApp até o portão. Os diagramas
estão em Mermaid, que o GitHub desenha sozinho.

## Visão geral

```mermaid
flowchart LR
    subgraph Pessoas
        U["Contatos e grupos<br/>(WhatsApp)"]
        ADM["Administrador<br/>(navegador)"]
    end

    WA[("Servidores do<br/>WhatsApp")]

    subgraph Docker["docker compose"]
        subgraph PZ["pingzap (Go)"]
            WM["whatsapp<br/>(whatsmeow, aparelho conectado)"]
            BOT["bot<br/>roteia chat → bot"]
            FLOW["flow<br/>máquina de estados"]
            WEB["web<br/>API + React"]
            MQ["mqtt<br/>(paho)"]
            MET["metrics"]
            DB[("SQLite<br/>usuários, dispositivos,<br/>bots, conversas")]
        end
        MOS{{"Mosquitto<br/>:1883, com login"}}
        INF[("InfluxDB")]
        GRA["Grafana :3000"]
    end

    subgraph Placa["Placa pingzap (hardware/)"]
        ESP["ESP32<br/>firmware/pingzap"]
        REL["Relé D23"]
        OPT["Opto D5"]
        BZ["Buzzer D4"]
        LED["LED D2"]
    end

    PORTAO["Central do<br/>portão"]

    U <--> WA <--> WM
    WM --> BOT
    BOT <--> FLOW
    BOT <--> DB
    BOT --> MQ
    WEB <--> DB
    WEB --> WM
    ADM -- "QR code, bots,<br/>dispositivos" --> WEB
    MQ <--> MOS
    MOS <-- "cmd: PULSE / ON / OFF<br/>data: telemetria<br/>status: online/offline" --> ESP
    ESP -- "GET /api/device/config<br/>(token)" --> WEB
    BOT --> MET --> INF --> GRA
    ADM --> GRA
    ESP --> REL & OPT --> PORTAO
    ESP --> BZ & LED
```

- O **pingzap** entra no WhatsApp como aparelho conectado; o pareamento é
  feito pelo QR code na página *WhatsApp*.
- Cada contato ou grupo é ligado a um **bot**, e cada bot a um
  **dispositivo**. Esse vínculo fica no SQLite e é editado na web.
- O **ESP32** não tem Wi-Fi fixo no código: sem rede conhecida, abre a
  rede `pingzap-XXXX` com uma página para escolher o Wi-Fi, o broker, a API e
  o token. Depois busca na API, com o seu token, o client ID e os tópicos que
  deve usar. Depois só fala MQTT com o Mosquitto.
- A telemetria passa pelo pingzap: ele grava o último dado no SQLite (para a
  página *Dispositivos*) e envia a série ao InfluxDB, que o Grafana mostra.

A placa do ESP32 (relé e saída isolada para o portão, buzzer, LED e fonte),
desenhada em `hardware/`:

![Placa do pingzap em 3D, com o ESP32 encaixado](../hardware/render-3d.png)

## Tópicos MQTT

Os tópicos são cadastrados por dispositivo na web. O padrão sugerido é
`pingzap/<client_id>/...`.

| Tópico | Sentido | Conteúdo |
|---|---|---|
| comando (`.../cmd`) | pingzap → ESP32 | `PULSE` aciona o portão (relé D23 e opto D5 por 1 s)<br/>`ON` liga o alarme (LED D2 e buzzer D4 piscando)<br/>`OFF` desliga o alarme |
| telemetria (`.../data`) | ESP32 → pingzap | JSON a cada 5 s e a cada comando: `device`, `uptime_s`, `wifi_rssi`, `free_heap`, `alert`, `relay`, `event` |
| status (`.../status`) | ESP32 → pingzap | `online` ao conectar; `offline` pelo Last Will (retido) |

## Partida do ESP32

```mermaid
sequenceDiagram
    participant E as ESP32
    participant A as pingzap API
    participant M as Mosquitto
    alt rede salva encontrada
        E->>E: conecta no Wi-Fi (LED pisca 3x)
    else nenhuma rede conhecida (ou BOOT por 3 s)
        E->>E: abre a rede pingzap-XXXX (LED aceso)
        Note over E: no celular: escolher o Wi-Fi,<br/>broker, API e token (gravados na flash)
    end
    E->>A: GET /api/device/config (Authorization: Bearer token)
    A-->>E: client_id, cmd_topic, data_topic, status_topic
    E->>M: CONNECT (usuário/senha, LWT "offline" em status)
    E->>M: PUBLISH status = online (retido)
    E->>M: SUBSCRIBE cmd_topic
    loop a cada 5 s
        E->>M: PUBLISH data_topic {telemetria}
    end
    loop a cada 60 s
        E->>A: busca a configuração de novo (reconecta se mudou)
    end
```

## Bot de fluxo: abrir o portão

Mesmo fluxo para `/start` → menu → `1`, ou direto pelo atalho `/abrir`.
Mensagens com mais de 1 minuto são descartadas antes de chegar ao bot: ao
reconectar, o WhatsApp entrega o que chegou com o pingzap fora do ar, e esses
comandos antigos não podem pulsar o portão.

```mermaid
sequenceDiagram
    actor P as Pessoa (WhatsApp)
    participant B as pingzap (bot + flow)
    participant M as Mosquitto
    participant E as ESP32
    participant G as Portão
    P->>B: /abrir
    B->>M: PUBLISH cmd = PULSE
    M->>E: PULSE
    E->>G: relé D23 + opto D5 por 1 s
    E->>M: telemetria (event: PULSE)
    B->>P: "O portão abriu? 1 - Sim, 2 - Não"
    alt 2 - Não
        P->>B: 2
        B->>M: PULSE (nova tentativa, até o limite)
        B->>P: "O portão abriu?"
    else sem resposta
        B->>P: lembrete a cada 3 min
        B->>M: após 3 lembretes: ON (alarme de urgência)
        M->>E: LED + buzzer
    end
    P->>B: 1
    B->>B: estado = aberto
    B->>M: OFF (se o alarme estava ligado)
    B->>P: "Portão aberto."
```

## Máquina de estados do portão

O estado é um só por bot, compartilhado por todos os contatos e grupos dele.

```mermaid
stateDiagram-v2
    [*] --> fechado
    fechado --> aguardando_abrir: 1 ou /abrir (PULSE)
    aguardando_abrir --> aguardando_abrir: 2 - Não (PULSE de novo)
    aguardando_abrir --> aberto: 1 - Sim
    aguardando_abrir --> fechado: limite de tentativas
    fechado --> aberto: "aberto pelo controle"
    aberto --> aguardando_fechar: 1 ou /fechar (PULSE)
    aguardando_fechar --> aguardando_fechar: 2 - Não (PULSE de novo)
    aguardando_fechar --> fechado: 1 - Sim
    aguardando_fechar --> aberto: limite de tentativas
    aberto --> fechado: "fechado pelo controle"

    note right of aguardando_abrir
        sem resposta: lembrete a cada 3 min;
        no 3º lembrete o alarme (ON) dispara;
        a resposta desliga o alarme (OFF)
    end note
```

## Bot de alerta

```mermaid
sequenceDiagram
    actor C as Contato configurado
    actor Eu as Dono da conta
    participant B as pingzap
    participant E as ESP32
    C->>B: mensagem (com a palavra-chave, se houver)
    B->>E: ON (via MQTT)
    E->>E: LED D2 + buzzer D4 piscando
    Eu->>C: respondo pelo WhatsApp
    B->>E: OFF (quando todos os alertas abertos foram respondidos)
```

## Onde fica cada parte

| Pasta | Conteúdo |
|---|---|
| `cmd/pingzap` | ponto de entrada: liga store, MQTT, WhatsApp, bots, métricas e HTTP |
| `internal/whatsapp` | conexão whatsmeow, QR code, contatos e grupos, envio |
| `internal/bot` | roteamento chat → bot, bots de fluxo e de alerta, lembretes, telemetria |
| `internal/flow` | máquina de estados pura (menus, atalhos, confirmação, alarme) |
| `internal/store` | SQLite: usuários, sessões, dispositivos, bots, conversas |
| `internal/web` + `web/` | API em Go e interface em React (embutida no binário) |
| `internal/mqtt` | cliente MQTT com reconexão e assinaturas dinâmicas |
| `internal/metrics` | envio da telemetria ao InfluxDB (line protocol, em lote) |
| `mosquitto/`, `grafana/` | configuração do broker e do dashboard |
| `firmware/pingzap` | firmware do ESP32 |
| `hardware/` | placa do ESP32: esquemático, Gerbers para fabricação, BOM, desenho 3D |
