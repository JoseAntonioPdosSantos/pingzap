# pingzap

[English](README.md) | **Português (Brasil)**

Ponte entre o WhatsApp e um ESP32: quando um dos números configurados me envia
uma mensagem, o pingzap publica `ON` em um broker MQTT e o ESP32, que está
inscrito nesse tópico, pisca o LED em sinal de urgência. Quando eu respondo a
essa pessoa, o pingzap publica `OFF` e o LED apaga.

## Fluxo

```
 contato               pingzap (Go)                broker MQTT             ESP32
    │                       │                           │                     │
    │── mensagem ──────────▶│                           │                     │
    │   (WhatsApp)          │ 1. é de um número         │                     │
    │                       │    configurado?           │                     │
    │                       │ 2. contém a palavra-chave │                     │
    │                       │    (se houver uma)?       │                     │
    │                       │── "ON" em ───────────────▶│── "ON" ────────────▶│
    │                       │   esp32/unit01/cmd        │                     │ LED piscando
    │                       │                           │                     │
    │◀── minha resposta ────│ 3. todos que dispararam   │                     │
    │    (de qualquer       │    já foram respondidos?  │                     │
    │     aparelho)         │── "OFF" em ──────────────▶│── "OFF" ───────────▶│
    │                       │   esp32/unit01/cmd        │                     │ LED apagado
    │                       │                           │                     │
    │                       │◀── esp32/unit01/data ─────│◀── estado do LED ───│
    │                       │    (só registra no log)   │    + telemetria     │
```

1. O pingzap se conecta ao WhatsApp como um **aparelho conectado** (mesma
   mecânica do WhatsApp Web), usando a biblioteca
   [whatsmeow](https://github.com/tulir/whatsmeow). A sessão fica em
   `whatsapp.db`.
2. **Ligar (`ON`)** — a cada mensagem recebida, dois filtros são aplicados:
   - **Número**: o remetente precisa estar em `PINGZAP_NUMBERS`. Mensagens de
     grupos são ignoradas.
   - **Palavra-chave**: se `PINGZAP_KEYWORD` estiver definida (ex.: `/urgente`),
     a mensagem precisa contê-la. Se estiver vazia, qualquer mensagem passa.

   Passando nos filtros, o pingzap publica `ON` no tópico de comando
   (`esp32/unit01/cmd`) com QoS 1 e marca o número como aguardando resposta.
3. **Desligar (`OFF`)** — quando eu envio uma mensagem para um dos números
   configurados, pelo celular ou por qualquer outro aparelho conectado, ele
   deixa de aguardar resposta. Se não sobrar nenhum número aguardando, o
   pingzap publica `OFF`. Assim, se duas pessoas dispararam o alerta, o LED só
   apaga depois que eu responder às duas. Qualquer mensagem minha conta como
   resposta; a palavra-chave não se aplica aqui.
4. O ESP32 (sketch em [`firmware/pingzap/pingzap.ino`](firmware/pingzap/pingzap.ino))
   pisca o LED da placa ao receber `ON` e o apaga ao receber `OFF`. A cada
   mudança devolve o estado no tópico de telemetria (`esp32/unit01/data`), que
   o pingzap apenas escreve no log.

## Arquitetura

```
pingzap/
├── cmd/
│   └── pingzap/
│       └── main.go          # ponto de entrada: carrega a config e liga as partes
├── internal/
│   ├── alert/
│   │   ├── alert.go         # controla quais números ainda aguardam resposta
│   │   └── alert_test.go
│   ├── config/
│   │   └── config.go        # lê .env / variáveis de ambiente e valida
│   ├── mqtt/
│   │   └── mqtt.go          # conexão com o broker, publicação de ON/OFF, log da telemetria
│   └── whatsapp/
│       ├── whatsapp.go      # sessão, pareamento por QR code e tratamento das mensagens
│       ├── filter.go        # regras de número e de palavra-chave
│       └── filter_test.go
├── docs/
│   ├── broker.md            # guia de configuração do broker
│   └── broker.pt-BR.md
├── firmware/
│   └── pingzap/
│       ├── pingzap.ino      # sketch do ESP32
│       └── secrets_example.h
├── .env_example
├── README.md
├── README.pt-BR.md
└── go.mod
```

Os pacotes `whatsapp`, `mqtt` e `alert` não se conhecem. O `whatsapp` recebe
dois callbacks — `OnTrigger` (chegou uma mensagem que dispara o alerta) e
`OnReply` (eu respondi a um número configurado) — e é o `main.go` que os liga ao
`alert.Tracker` e a `mqtt.PublishAlert` / `mqtt.PublishClear`.

O firmware fica em `firmware/`, fora do módulo Go: é outro toolchain (Arduino),
mas vive no mesmo repositório porque compartilha com o pingzap o contrato de
broker, tópicos e payloads.

## Configuração

A configuração vem de variáveis de ambiente. Para não precisar exportá-las, crie
um arquivo `.env` na pasta de onde o programa é executado:

```sh
cp .env_example .env
```

| Variável                   | Padrão                               | Descrição |
|----------------------------|--------------------------------------|-----------|
| `PINGZAP_NUMBERS`          | — (obrigatória)                      | Números que disparam o alerta, separados por vírgula. |
| `PINGZAP_KEYWORD`          | vazio                                | Palavra-chave exigida na mensagem. Vazio = qualquer mensagem dispara. |
| `PINGZAP_MQTT_BROKER`      | `wss://broker.emqx.io:8084/mqtt`     | URL do broker. |
| `PINGZAP_MQTT_USERNAME`    | vazio                                | Usuário do broker (dispensável em brokers públicos). |
| `PINGZAP_MQTT_PASSWORD`    | vazio                                | Senha do broker. |
| `PINGZAP_MQTT_CMD_TOPIC`   | `esp32/unit01/cmd`                   | Tópico onde os comandos são publicados. |
| `PINGZAP_MQTT_DATA_TOPIC`  | `esp32/unit01/data`                  | Tópico de telemetria do ESP32 (apenas log). |
| `PINGZAP_MQTT_ON_PAYLOAD`  | `ON`                                 | Conteúdo publicado para iniciar o alerta. |
| `PINGZAP_MQTT_OFF_PAYLOAD` | `OFF`                                | Conteúdo publicado para encerrar o alerta. |
| `PINGZAP_WHATSAPP_DB`      | `file:whatsapp.db?_foreign_keys=on`  | Banco SQLite com a sessão do WhatsApp. |

Variáveis já exportadas no shell têm precedência sobre o arquivo `.env`.

Para usar um broker próprio no lugar do público, ou para inspecionar os tópicos
com o MQTTX, veja o [guia de configuração do broker](docs/broker.pt-BR.md).

### Números

```sh
PINGZAP_NUMBERS=67999999999,67988888888
```

- O formato básico é DDD + telefone; o código do país (`55`) é adicionado
  automaticamente. Também é aceito o número completo e com pontuação, como
  `+55 (67) 99999-9999`.
- Contas brasileiras antigas são identificadas no WhatsApp sem o nono dígito;
  o pingzap aceita as duas formas, então basta informar o número normalmente.

### Palavra-chave

```sh
PINGZAP_KEYWORD=/urgente   # só mensagens que contenham /urgente
PINGZAP_KEYWORD=           # qualquer mensagem
```

- A comparação ignora maiúsculas/minúsculas e a palavra pode estar em qualquer
  ponto do texto (`Me liga, /URGENTE!` dispara).
- Vale para o texto da mensagem e para a legenda de fotos, vídeos e documentos.
- Com palavra-chave definida, mensagens sem texto (áudio, figurinha, foto sem
  legenda) não disparam. Sem palavra-chave, disparam.

## Como executar

Requisitos: Go e um compilador C (o driver SQLite usa cgo).

```sh
go run ./cmd/pingzap
```

Ou gerando o binário:

```sh
go build -o pingzap ./cmd/pingzap
./pingzap
```

Na **primeira execução** aparece um QR code no terminal: abra o WhatsApp no
celular em *Aparelhos conectados > Conectar um aparelho* e leia o código. Nas
seguintes a sessão salva em `whatsapp.db` é reutilizada. Para parear outra
conta, apague esse arquivo.

Execute sempre a partir da raiz do repositório, pois o `.env` e o `whatsapp.db` são
procurados no diretório atual.

Testes:

```sh
go test ./...
```

## ESP32

O sketch fica em `firmware/pingzap/pingzap.ino` e precisa das bibliotecas
`PubSubClient` e `ArduinoJson` (v7).

Antes de gravar, crie o arquivo com as credenciais do Wi-Fi (e o login do
broker, se o seu exigir):

```sh
cp firmware/pingzap/secrets_example.h firmware/pingzap/secrets.h
```

Depois abra a pasta `firmware/pingzap` na Arduino IDE (a pasta e o `.ino`
precisam ter o mesmo nome) e grave na placa.

### Simulador

Sem a placa em mãos? O firmware está disponível como uma
[simulação no Wokwi](https://wokwi.com/projects/477507248547038209): abra o link e clique em
play para rodar um ESP32 virtual no navegador. Ele se conecta ao mesmo broker e
aos mesmos tópicos, então reage aos comandos `ON` / `OFF` publicados pelo
pingzap como a placa real.

### Tópicos

| Tópico                | Direção          | Conteúdo |
|-----------------------|------------------|----------|
| `esp32/unit01/cmd`    | pingzap → ESP32  | `ON` inicia o alerta (LED piscando); `OFF` encerra. |
| `esp32/unit01/data`   | ESP32 → pingzap  | Telemetria em JSON a cada 5 s e o estado do LED. |
| `esp32/unit01/status` | ESP32 → broker   | `online` / `offline` (LWT, retido). |

O LED pisca até o `OFF` chegar. Para que ele também apague sozinho depois de um
tempo, ajuste `ALERT_DURATION` no sketch (em milissegundos; `0` = sem limite).

Se mudar os tópicos, os payloads ou o broker no `.env`, mude também no sketch —
os dois lados precisam usar os mesmos valores. O pingzap fala com o broker por
WebSocket seguro (porta 8084) e o ESP32 por MQTT puro (porta 1883); é o mesmo
broker.

## Observações

- `broker.emqx.io` é um broker **público**: qualquer pessoa que conheça o tópico
  pode ler ou publicar nele. Para uso real, troque por um broker com
  autenticação (veja o [guia de configuração do broker](docs/broker.pt-BR.md))
  ou, no mínimo, use tópicos difíceis de adivinhar.
- `whatsapp.db` dá acesso à conta do WhatsApp, o `.env` contém números de
  telefone e o `secrets.h` as senhas do Wi-Fi e do broker; os três estão no `.gitignore` e
  não devem ser versionados.
- Os alertas pendentes ficam só em memória. Se o pingzap for reiniciado com o
  LED piscando, a próxima resposta a qualquer número configurado já publica
  `OFF`.
- Os comandos não são retidos no broker: se o ESP32 estiver desligado ou sem
  rede quando o `ON` ou o `OFF` for publicado, ele não recebe esse comando.
