package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"iag-mes/backend/internal/config"
	"iag-mes/backend/internal/events"
)

func main() {
	cfg := config.Load()
	pub := events.NewPublisher(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaClientID)
	defer pub.Close()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": cfg.ServiceName})
	})
	r.GET("/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	v1 := r.Group("/api/v1")
	{
		v1.POST("/production-orders", func(c *gin.Context) {
			var body struct {
				BatchBusinessID string  `json:"batch_business_id" binding:"required"`
				Stage           string  `json:"stage" binding:"required"`
				Facility        string  `json:"facility"`
				KgIn, KgOut     float64 `json:"kg_in"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			eventType := mapStageEvent(body.Stage)
			if eventType == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown stage"})
				return
			}
			data := map[string]any{
				"batch_business_id": body.BatchBusinessID,
				"facility":          body.Facility,
				"kg_in":             body.KgIn,
				"kg_out":            body.KgOut,
			}
			if err := pub.Publish(c.Request.Context(), eventType, data); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "kafka publish failed"})
				return
			}
			c.JSON(http.StatusCreated, gin.H{"status": "published", "event_type": eventType})
		})
	}

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r, ReadHeaderTimeout: 10 * time.Second}
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

func mapStageEvent(stage string) string {
	switch stage {
	case "wetmill", "wet_mill":
		return "mes.wetmill.completed"
	case "drying", "dry":
		return "mes.drying.completed"
	case "drymill", "dry_mill":
		return "mes.drymill.completed"
	default:
		return ""
	}
}
