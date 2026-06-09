package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/db"
	"iag-mes/backend/internal/jobs"
	"iag-mes/backend/internal/migrate"
	"iag-mes/backend/internal/store"
)

func main() {
	defaultPlant := strings.TrimSpace(os.Getenv("MES_JOBS_PLANT"))
	if defaultPlant == "" {
		defaultPlant = "kampala"
	}

	plant := flag.String("plant", defaultPlant, "plant code for KPI rollup and energy insights")
	doKPI := flag.Bool("kpi", false, "rollup KPI snapshots")
	doAlerts := flag.Bool("alerts", false, "evaluate telemetry alert rules")
	doAI := flag.Bool("ai", false, "generate AI recommendations")
	doEnergy := flag.Bool("energy", false, "generate energy optimization insights")
	doPreventiveMaintenance := flag.Bool("preventive-maintenance", false, "sync preventive maintenance schedules and auto-generate work orders")
	doAll := flag.Bool("all", true, "run all jobs (when no specific job flag is set)")
	daemon := flag.Bool("daemon", false, "run scheduled jobs on intervals until SIGTERM")
	loop := flag.Duration("loop", 0, "rerun selected jobs on this interval (0 = run once); ignored when -daemon")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	st := store.New(pool)

	opts := runOpts{
		plant:                 *plant,
		runAll:                *doAll && !*doKPI && !*doAlerts && !*doAI && !*doEnergy && !*doPreventiveMaintenance,
		kpi:                   *doKPI,
		alerts:                *doAlerts,
		ai:                    *doAI,
		energy:                *doEnergy,
		preventiveMaintenance: *doPreventiveMaintenance,
	}

	if *daemon {
		runDaemon(ctx, st, opts)
		return
	}

	if *loop > 0 {
		runLoop(ctx, st, opts, *loop)
		return
	}

	if err := runOnce(ctx, st, opts); err != nil {
		log.Fatalf("mes-jobs: %v", err)
	}
}

type runOpts struct {
	plant                 string
	runAll                bool
	kpi                   bool
	alerts                bool
	ai                    bool
	energy                bool
	preventiveMaintenance bool
}

func runOnce(ctx context.Context, st *store.Store, o runOpts) error {
	if o.runAll || o.kpi {
		n, err := jobs.RollupKPIs(ctx, st, o.plant)
		if err != nil {
			return err
		}
		log.Printf("mes-jobs: wrote %d KPI snapshots for plant %s", n, o.plant)
	}
	if o.runAll || o.alerts {
		n, err := jobs.EvaluateTelemetryAlerts(ctx, st)
		if err != nil {
			return err
		}
		log.Printf("mes-jobs: created %d alerts", n)
	}
	if o.runAll || o.ai {
		n, err := jobs.GenerateAIRecommendations(ctx, st)
		if err != nil {
			return err
		}
		log.Printf("mes-jobs: created %d AI recommendations", n)
	}
	if o.runAll || o.energy {
		n, err := jobs.GenerateEnergyInsights(ctx, st, o.plant)
		if err != nil {
			return err
		}
		log.Printf("mes-jobs: created %d energy insights", n)
	}
	if o.runAll || o.preventiveMaintenance {
		created, overdue, err := jobs.SyncPreventiveMaintenance(ctx, st)
		if err != nil {
			return err
		}
		log.Printf("mes-jobs: preventive maintenance sync — %d overdue, %d work orders created", overdue, created)
	}
	return nil
}

func runLoop(ctx context.Context, st *store.Store, o runOpts, interval time.Duration) {
	log.Printf("mes-jobs: loop every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := runOnce(ctx, st, o); err != nil {
			log.Printf("mes-jobs: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type scheduledJob struct {
	name     string
	interval time.Duration
	opts     runOpts
}

func runDaemon(ctx context.Context, st *store.Store, base runOpts) {
	schedules := []scheduledJob{
		{name: "telemetry-alerts", interval: 15 * time.Minute, opts: runOpts{alerts: true, plant: base.plant}},
		{name: "kpi-rollup", interval: time.Hour, opts: runOpts{kpi: true, plant: base.plant}},
		{name: "preventive-maintenance-sync", interval: time.Hour, opts: runOpts{preventiveMaintenance: true, plant: base.plant}},
		{name: "ai-recommendations", interval: 24 * time.Hour, opts: runOpts{ai: true, plant: base.plant}},
		{name: "energy-insights", interval: 24 * time.Hour, opts: runOpts{energy: true, plant: base.plant}},
	}

	log.Printf("mes-jobs: daemon started (plant=%s)", base.plant)
	for _, s := range schedules {
		startScheduled(ctx, st, s)
	}
	<-ctx.Done()
	log.Printf("mes-jobs: daemon stopped")
}

func startScheduled(ctx context.Context, st *store.Store, s scheduledJob) {
	run := func() {
		if err := runOnce(ctx, st, s.opts); err != nil {
			log.Printf("mes-jobs %s: %v", s.name, err)
		}
	}
	run()
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
