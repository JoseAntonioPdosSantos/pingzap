package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"pingzap/internal/alert"
	"pingzap/internal/config"
	"pingzap/internal/mqtt"
	"pingzap/internal/whatsapp"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	if cfg.WhatsApp.Keyword == "" {
		log.Printf("watching %d number(s); any message triggers the alert", len(cfg.WhatsApp.Numbers))
	} else {
		log.Printf("watching %d number(s); only messages containing %q trigger the alert", len(cfg.WhatsApp.Numbers), cfg.WhatsApp.Keyword)
	}

	mq, err := mqtt.Connect(cfg.MQTT)
	if err != nil {
		log.Fatalf("MQTT error: %v", err)
	}

	pending := alert.NewTracker()
	wa, err := whatsapp.Connect(ctx, cfg.WhatsApp, whatsapp.Handlers{
		OnTrigger: func(number string) {
			pending.Raise(number)
			if err := mq.PublishAlert(); err != nil {
				log.Printf("failed to %v", err)
			}
		},
		// The LED only turns off once everyone who triggered the alert got a reply.
		OnReply: func(number string) {
			if n := pending.Ack(number); n > 0 {
				log.Printf("%d number(s) still awaiting a reply; alert kept on", n)
				return
			}
			if err := mq.PublishClear(); err != nil {
				log.Printf("failed to %v", err)
			}
		},
	})
	if err != nil {
		log.Fatalf("WhatsApp error: %v", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	log.Println("shutting down...")
	wa.Disconnect()
	mq.Disconnect()
}
