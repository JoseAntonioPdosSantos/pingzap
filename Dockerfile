# Interface web (React): compilada aqui e embutida no binário Go.
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# O build grava em ../internal/web/dist, ou seja, /src/internal/web/dist.
RUN npm run build

# go-sqlite3 precisa de CGO, então o build usa a imagem completa (com gcc).
FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/pingzap ./cmd/pingzap

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/pingzap /usr/local/bin/pingzap

# Os bancos (sessão do WhatsApp e dados do pingzap) ficam em /data, montado
# como volume pelo compose.
RUN mkdir /data && chown 1000:1000 /data
USER 1000:1000
WORKDIR /data
ENV PINGZAP_WHATSAPP_DB="file:/data/whatsapp.db?_foreign_keys=on" \
    PINGZAP_DB="file:/data/pingzap.db?_foreign_keys=on" \
    PINGZAP_HTTP_ADDR=":8080"
EXPOSE 8080

ENTRYPOINT ["pingzap"]
