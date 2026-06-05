package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"iag-mes/backend/internal/auditlog"
	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/events"
	"iag-mes/backend/internal/handlers"
)

func main() {
	cfg := config.Load()
	pub := events.NewPublisher(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaClientID)
	defer pub.Close()

	auditStore := auditlog.NewMemoryStore(500)
	router := handlers.NewRouter(handlers.RouterDeps{
		Cfg:   cfg,
		Pub:   pub,
		Audit: auditStore,
	})

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: router, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("mes listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
