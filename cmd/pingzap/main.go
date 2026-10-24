package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pingzap/internal/bot"
	"pingzap/internal/config"
	"pingzap/internal/metrics"
	"pingzap/internal/mqtt"
	"pingzap/internal/store"
	"pingzap/internal/web"
	"pingzap/internal/whatsapp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	st, err := store.Open(cfg.DB)
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer st.Close()
	if err := bot.ImportLegacy(st, cfg.Legacy); err != nil {
		log.Fatalf("import the .env alert: %v", err)
	}

	// The handlers refer to bots, created once both connections exist; no
	// message arrives before Reload subscribes and Start connects.
	var bots *bot.Service
	mq := mqtt.Connect(cfg.MQTT, func(topic string, payload []byte) {
		bots.HandleTelemetry(topic, payload)
	})
	wa, err := whatsapp.New(ctx, cfg.WhatsAppDB, func(m whatsapp.Message) {
		bots.HandleMessage(m)
	})
	if err != nil {
		log.Fatalf("WhatsApp error: %v", err)
	}
	var sink bot.Metrics
	if m := metrics.New(cfg.Influx); m != nil {
		log.Printf("writing telemetry to InfluxDB at %s", cfg.Influx.URL)
		go m.Run(ctx)
		sink = m
	}
	bots = bot.New(st, mq, wa, sink)
	if err := bots.Reload(); err != nil {
		log.Fatalf("load bots: %v", err)
	}
	if err := wa.Start(); err != nil {
		log.Printf("WhatsApp error: %v", err)
	}
	go bots.RunReminders(ctx)

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: web.New(st, bots, wa)}
	go func() {
		log.Printf("web interface listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("web server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	wa.Close()
	mq.Disconnect()
}
