# go-sqlite3 precisa de CGO, então o build usa a imagem completa (com gcc).
FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/pingzap ./cmd/pingzap

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/pingzap /usr/local/bin/pingzap

# A sessão do WhatsApp fica em /data, montado como volume pelo compose.
RUN mkdir /data && chown 1000:1000 /data
USER 1000:1000
WORKDIR /data
ENV PINGZAP_WHATSAPP_DB="file:/data/whatsapp.db?_foreign_keys=on"

ENTRYPOINT ["pingzap"]
