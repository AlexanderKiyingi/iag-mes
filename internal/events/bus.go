package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"

	"iag-mes/backend/internal/outbox"
)

type PlatformEvent struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Time          string         `json:"time"`
	Source        string         `json:"source"`
	SpecVersion   string         `json:"specversion"`
	CorrelationID string         `json:"correlationId,omitempty"`
	CausationID   string         `json:"causationId,omitempty"`
	Data          map[string]any `json:"data"`
}

type Bus struct {
	writers map[string]*kafka.Writer
	enabled bool
	outbox  *outbox.Store
	topics  struct {
		production string
		operations string
	}
	mu sync.RWMutex
}

type Config struct {
	Brokers          []string
	Enabled          bool
	ProductionTopic  string
	OperationsTopic  string
}

func New(cfg Config) *Bus {
	if !cfg.Enabled || len(cfg.Brokers) == 0 {
		return &Bus{enabled: false}
	}
	prodTopic := cfg.ProductionTopic
	if prodTopic == "" {
		prodTopic = TopicProduction
	}
	opsTopic := cfg.OperationsTopic
	if opsTopic == "" {
		opsTopic = TopicOperations
	}
	b := &Bus{
		enabled: true,
		writers: make(map[string]*kafka.Writer),
	}
	b.topics.production = prodTopic
	b.topics.operations = opsTopic
	for _, topic := range []string{prodTopic, opsTopic} {
		b.writers[topic] = &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        topic,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireAll,
			Transport:    &kafka.Transport{ClientID: Source},
		}
	}
	return b
}

func (b *Bus) SetOutbox(store *outbox.Store) {
	if b != nil {
		b.outbox = store
	}
}

func (b *Bus) Enabled() bool { return b != nil && b.enabled }

func (b *Bus) Close() error {
	if b == nil || !b.enabled {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var err error
	for _, w := range b.writers {
		if w == nil {
			continue
		}
		if e := w.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func (b *Bus) Publish(ctx context.Context, eventType string, data map[string]any, key string) {
	if b == nil || !b.enabled {
		return
	}
	topic := b.resolveTopic(eventType)
	evt := b.newEvent(eventType, data)
	if b.outbox != nil {
		if err := b.outbox.Enqueue(ctx, topic, eventType, key, evt); err != nil {
			slog.Warn("mes event enqueue failed", "type", eventType, "err", err)
		}
		return
	}
	if err := b.publishDirect(ctx, topic, evt, key); err != nil {
		slog.Warn("mes event publish failed", "type", eventType, "err", err)
	}
}

func (b *Bus) PublishTx(ctx context.Context, tx pgx.Tx, eventType string, data map[string]any, key string) error {
	if b == nil || !b.enabled {
		return nil
	}
	topic := b.resolveTopic(eventType)
	evt := b.newEvent(eventType, data)
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO mes_event_outbox (kafka_topic, event_type, event_key, payload)
		VALUES ($1, $2, $3, $4::jsonb)
	`, topic, eventType, nullableKey(key), body)
	return err
}

func (b *Bus) DispatchOutbox(ctx context.Context, row outbox.Row) error {
	if !b.enabled {
		return nil
	}
	var evt PlatformEvent
	if err := json.Unmarshal(row.Payload, &evt); err != nil {
		return fmt.Errorf("decode outbox payload: %w", err)
	}
	if evt.Type == "" {
		evt.Type = row.EventType
	}
	if evt.ID == "" {
		evt.ID = uuid.NewString()
	}
	if evt.Source == "" {
		evt.Source = Source
	}
	if evt.SpecVersion == "" {
		evt.SpecVersion = SpecVersion
	}
	if evt.Time == "" {
		evt.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	topic := row.KafkaTopic
	if topic == "" {
		topic = b.resolveTopic(evt.Type)
	}
	key := row.EventKey
	if key == "" {
		key = evt.ID
	}
	return b.publishDirect(ctx, topic, evt, key)
}

func (b *Bus) publishDirect(ctx context.Context, topic string, evt PlatformEvent, key string) error {
	b.mu.RLock()
	w := b.writers[topic]
	b.mu.RUnlock()
	if w == nil {
		return fmt.Errorf("no kafka writer for topic %q", topic)
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if key == "" {
		key = evt.ID
	}
	return w.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(evt.Type)},
			{Key: "ce-source", Value: []byte(evt.Source)},
		},
	})
}

func (b *Bus) resolveTopic(eventType string) string {
	if b == nil {
		return TopicProduction
	}
	t := TopicForEvent(eventType)
	switch t {
	case TopicOperations:
		if b.topics.operations != "" {
			return b.topics.operations
		}
	case TopicProduction:
		if b.topics.production != "" {
			return b.topics.production
		}
	}
	return t
}

func (b *Bus) newEvent(eventType string, data map[string]any) PlatformEvent {
	return PlatformEvent{
		ID:          uuid.NewString(),
		Type:        eventType,
		Time:        time.Now().UTC().Format(time.RFC3339Nano),
		Source:      Source,
		SpecVersion: SpecVersion,
		Data:        data,
	}
}

func nullableKey(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
