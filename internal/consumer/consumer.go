package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/segmentio/kafka-go"

	"iag-mes/backend/internal/store"
)

type Config struct {
	Brokers          []string
	GroupID          string
	SupplyChainTopic string
	QualityTopic     string
	OperationsTopic  string
}

type Consumer struct {
	cfg   Config
	store *store.Store
}

func New(cfg Config, st *store.Store) *Consumer {
	return &Consumer{cfg: cfg, store: st}
}

func (c *Consumer) Run(ctx context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		return nil
	}
	topics := []string{}
	for _, t := range []string{c.cfg.SupplyChainTopic, c.cfg.QualityTopic, c.cfg.OperationsTopic} {
		if strings.TrimSpace(t) != "" {
			topics = append(topics, t)
		}
	}
	if len(topics) == 0 {
		topics = []string{"iag.supply-chain"}
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     c.cfg.Brokers,
		GroupID:     c.cfg.GroupID,
		GroupTopics: topics,
		MinBytes:    1,
		MaxBytes:    10e6,
	})
	defer r.Close()

	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("mes consumer fetch: %v", err)
			continue
		}
		c.handleMessage(ctx, msg.Topic, msg.Value)
		if err := r.CommitMessages(ctx, msg); err != nil {
			log.Printf("mes consumer commit: %v", err)
		}
	}
}

func (c *Consumer) handleMessage(ctx context.Context, topic string, raw []byte) {
	var env struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}
	t := strings.ToLower(env.Type)
	batchID, _ := env.Data["batch_business_id"].(string)

	switch {
	case strings.Contains(topic, "supply-chain") || strings.HasPrefix(t, "scm."):
		if batchID == "" {
			return
		}
		switch t {
		case "scm.intake.received", "scm.batch.stage_changed", "scm.batch.created":
			_ = c.store.UpsertBatchRef(ctx, batchID, "kafka:"+env.Type)
		}
	case strings.Contains(topic, "quality") || strings.HasPrefix(t, "qc."):
		if batchID == "" {
			return
		}
		if t == "qc.lab.result_recorded" || t == "qc.coa.issued" {
			_ = c.store.UpsertBatchRef(ctx, batchID, "kafka:"+env.Type)
		}
	case strings.Contains(topic, "operations") || strings.HasPrefix(t, "warehouse."):
		if t == "warehouse.production.output" && batchID != "" {
			_ = c.store.UpsertBatchRef(ctx, batchID, "kafka:warehouse.output")
		}
	}
}
