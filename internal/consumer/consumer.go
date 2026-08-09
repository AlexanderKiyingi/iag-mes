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
		if err := c.handleMessage(ctx, msg.Topic, msg.Value); err != nil {
			log.Printf("mes consumer handle topic=%s: %v", msg.Topic, err)
			// Leave it uncommitted so it is retried after a restart. The
			// registration is an idempotent upsert, so reapplying costs nothing,
			// and committing over the failure loses it outright.
			continue
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			log.Printf("mes consumer commit: %v", err)
		}
	}
}

// handleMessage registers the batch a cross-domain event refers to.
//
// The outcome used to be discarded and the offset committed regardless, so a
// failed registration was logged and lost. The write is an upsert keyed on the
// batch id, which makes reapplying it free — there was never anything to gain
// by swallowing the error.
func (c *Consumer) handleMessage(ctx context.Context, topic string, raw []byte) error {
	var env struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		// Undecodable: retrying cannot help, so let the caller commit past it
		// rather than block the partition on it forever.
		log.Printf("mes consumer: undecodable message on topic=%s: %v", topic, err)
		return nil
	}
	t := strings.ToLower(env.Type)
	batchID, _ := env.Data["batch_business_id"].(string)

	switch {
	case strings.Contains(topic, "supply-chain") || strings.HasPrefix(t, "scm."):
		if batchID == "" {
			return nil
		}
		switch t {
		case "scm.intake.received", "scm.batch.stage_changed", "scm.batch.created":
			return c.store.UpsertBatchRef(ctx, batchID, "kafka:"+env.Type)
		}
	case strings.Contains(topic, "quality") || strings.HasPrefix(t, "qc."):
		if batchID == "" {
			return nil
		}
		if t == "qc.lab.result_recorded" || t == "qc.coa.issued" {
			return c.store.UpsertBatchRef(ctx, batchID, "kafka:"+env.Type)
		}
	case strings.Contains(topic, "operations") || strings.HasPrefix(t, "warehouse."):
		if t == "warehouse.production.output" && batchID != "" {
			return c.store.UpsertBatchRef(ctx, batchID, "kafka:warehouse.output")
		}
	}
	return nil
}
