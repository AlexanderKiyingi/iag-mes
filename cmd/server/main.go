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
	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/events"
	"iag-mes/backend/internal/handlers"
	"iag-mes/backend/internal/migrate"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		if err := migrate.Up(ctx, pool); err != nil {
			log.Fatalf("migrate: %v", err)
		}
	}

	pub := events.NewPublisher(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaClientID)
	defer pub.Close()

	auditStore := auditlog.NewStore(pool)
	router := handlers.NewRouter(handlers.RouterDeps{
		Cfg:   cfg,
		Pool:  pool,
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
