package store

import "context"

// Production runs, stage events and CCP readings live in iag-production
// (:4002). What remains here is the batch registry the Kafka consumer and
// the SCM validation write to.

func (s *Store) UpsertBatchRef(ctx context.Context, batchBusinessID, source string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mes_batch_refs (batch_business_id, source, validated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (batch_business_id) DO UPDATE SET validated_at = NOW(), source = EXCLUDED.source`,
		batchBusinessID, source)
	return err
}
