package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	platformotel "github.com/alvor-technologies/iag-platform-go/otel"

	"iag-mes/backend/internal/auditlog"
	"iag-mes/backend/internal/clients"
	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/events"
	"iag-mes/backend/internal/handlers"
	"iag-mes/backend/internal/integrations"
	"iag-mes/backend/internal/middleware"
	"iag-mes/backend/internal/migrate"
	"iag-mes/backend/internal/outbox"
	"iag-mes/backend/internal/store"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// OpenTelemetry → otel-collector:4317 (non-blocking dial).
	if tp, err := platformotel.Init(ctx, platformotel.Config{
		ServiceName: cfg.ServiceName,
		Environment: cfg.Environment,
	}); err != nil {
		log.Printf("otel disabled: %v", err)
	} else {
		defer func() {
			sc, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tp.Shutdown(sc)
		}()
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

	st := store.New(pool)
	auditStore := auditlog.NewStore(pool)
	outboxStore := outbox.NewStore(pool)

	bus := events.New(events.Config{
		Brokers:         cfg.KafkaBrokers,
		Enabled:         cfg.EventBusEnabled && len(cfg.KafkaBrokers) > 0,
		ProductionTopic: cfg.KafkaProductionTopic,
		OperationsTopic: cfg.KafkaOperationsTopic,
	})
	bus.SetOutbox(outboxStore)
	st.SetEventBus(bus)
	defer bus.Close()

	if bus.Enabled() {
		pub := outbox.NewPublisher(outboxStore, bus)
		go pub.Run(ctx)
	}

	var verifier *authclient.Verifier
	if cfg.AuthMode == "jwt" {
		verifier = authclient.NewVerifier(authclient.Options{
			JWKSURL:  cfg.JWKSURL,
			Issuer:   cfg.JWTIssuer,
			Audience: cfg.Audience,
		})
		// Tolerate transient JWKS failure on boot. A hard exit here turns an
		// auth service redeploy into a crash loop for this service, and the
		// container never gets far enough to serve /health — which reads as
		// "mes is down" rather than "auth was briefly unavailable".
		bootstrapJWKS(verifier)
		go jwksRefreshLoop(ctx, verifier)
	}

	platformAuth := middleware.NewPlatformAuth(middleware.PlatformAuthOptions{
		Mode:     cfg.AuthMode,
		Verifier: verifier,
	})

	if cfg.AuthMode == "jwt" && cfg.ServiceClientSecret != "" {
		go registerPermissionsLoop(ctx, cfg)
	} else if cfg.AuthMode == "jwt" {
		log.Printf("mes: SERVICE_CLIENT_SECRET unset — skipping permissions registration")
	}

	var bridge *integrations.Bridge
	if cfg.IntegrationsEnabled {
		bridge = &integrations.Bridge{
			Warehouse: clients.NewWarehouse(cfg.UpstreamWarehouse, cfg.AuthTokenURL, cfg.ServiceClientID, cfg.ServiceClientSecret),
			QC:        clients.NewQualityControl(cfg.UpstreamQC, cfg.AuthTokenURL, cfg.ServiceClientID, cfg.ServiceClientSecret),
			ERP:       clients.NewERP(cfg.UpstreamERP, cfg.AuthTokenURL, cfg.ServiceClientID, cfg.ServiceClientSecret),
			SCM:       clients.NewSCM(cfg.UpstreamSCM, cfg.AuthTokenURL, cfg.ServiceClientID, cfg.ServiceClientSecret),
			Store:     st,
			Cfg: integrations.Config{
				AutoWarehouseOnComplete: cfg.AutoWarehouseOnRunComplete,
				AutoQCOnComplete:        cfg.AutoQCOnRunComplete,
				AutoValidateBatch:       cfg.AutoValidateBatchWithSCM,
			},
		}
	}

	api := &handlers.API{Cfg: cfg, Store: st, Audit: auditStore, Bus: bus, Pool: pool, Bridge: bridge}
	router := handlers.NewRouter(handlers.RouterDeps{
		API:          api,
		Audit:        auditStore,
		PlatformAuth: platformAuth,
		CORSOrigins:  cfg.CORSOrigins,
		StrictRBAC:   cfg.StrictRBAC(),
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("mes listening on :%s (aud=%s integrations=%v)", cfg.Port, cfg.Audience, cfg.IntegrationsEnabled)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// bootstrapJWKS performs the initial JWKS fetch with exponential backoff so a
// transient failure (auth cold start, redeploy, network blip) does not kill the
// container. Budget ~2 minutes; returns once keys are loaded or the budget is
// spent, and the caller starts the refresh loop either way.
func bootstrapJWKS(v *authclient.Verifier) {
	backoff := time.Second
	const (
		maxBackoff   = 15 * time.Second
		totalBudget  = 2 * time.Minute
		perAttemptTO = 10 * time.Second
	)
	deadline := time.Now().Add(totalBudget)
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(context.Background(), perAttemptTO)
		err := v.Refresh(attemptCtx)
		cancel()
		if err == nil {
			log.Printf("jwks bootstrap ok (attempt %d)", attempt)
			return
		}
		if time.Now().After(deadline) {
			log.Printf("jwks bootstrap budget exhausted after %d attempts; continuing with empty key set: %v", attempt, err)
			return
		}
		log.Printf("jwks bootstrap failed (attempt %d), retrying in %s: %v", attempt, backoff, err)
		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// jwksRefreshLoop keeps the key set current at two speeds: the steady rotation
// interval once keys are loaded, and a much shorter one while the set is empty.
// Empty is not a mild degradation — every authenticated request fails closed —
// so recovery has to be seconds, not a quarter of an hour.
func jwksRefreshLoop(ctx context.Context, v *authclient.Verifier) {
	const (
		steadyInterval   = 15 * time.Minute
		degradedInterval = 15 * time.Second
	)
	for {
		wait := steadyInterval
		if !v.HasKeys() {
			wait = degradedInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		hadKeys := v.HasKeys()
		refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := v.Refresh(refreshCtx)
		cancel()
		switch {
		case err != nil && hadKeys:
			// Previous key set is still in memory; tokens keep verifying.
			log.Printf("jwks refresh failed; serving with the previous key set: %v", err)
		case err != nil:
			log.Printf("jwks still unavailable; all authenticated requests are being rejected: %v", err)
		case !hadKeys:
			log.Printf("jwks recovered; token verification restored")
		}
	}
}
