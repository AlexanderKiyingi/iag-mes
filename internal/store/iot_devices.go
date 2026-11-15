package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// IoT device registry for edge/machine-telemetry.
//
// Hashing must match github.com/iag/machine-telemetry (sha256 hex of trimmed
// plaintext) so keys issued here authenticate on HTTP ingest.

var (
	ErrIoTDeviceNotFound = errors.New("iot device not found")
	ErrIoTUnknownAsset   = errors.New("unknown assetTag — register the machine on Assets first")
)

type IoTDevice struct {
	ID        int64      `json:"id"`
	Serial    string     `json:"serial"`
	Label     string     `json:"label,omitempty"`
	AssetTag  string     `json:"assetTag,omitempty"`
	HasAPIKey bool       `json:"hasApiKey"`
	IsActive  bool       `json:"isActive"`
	LastSeen  *time.Time `json:"lastSeen,omitempty"`
	LastIP    string     `json:"lastIp,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

type CreatedIoTDevice struct {
	IoTDevice
	APIKeyPlaintext string `json:"apiKey,omitempty"`
}

type CreateIoTDeviceInput struct {
	Serial   string
	Label    string
	AssetTag string
	IssueKey bool
}

type UpdateIoTDeviceInput struct {
	Label    *string
	AssetTag *string
	IsActive *bool
}

func hashMachineAPIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plaintext)))
	return hex.EncodeToString(sum[:])
}

func (s *Store) ensureAssetTag(ctx context.Context, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil
	}
	var one int
	err := s.pool.QueryRow(ctx, `SELECT 1 FROM mes_assets WHERE tag = $1`, tag).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIoTUnknownAsset
	}
	return err
}

func (s *Store) CreateIoTDevice(ctx context.Context, in CreateIoTDeviceInput) (*CreatedIoTDevice, error) {
	serial := strings.TrimSpace(in.Serial)
	if serial == "" {
		return nil, errors.New("serial required")
	}
	assetTag := strings.TrimSpace(in.AssetTag)
	if err := s.ensureAssetTag(ctx, assetTag); err != nil {
		return nil, err
	}
	var keyHash, plaintext string
	if in.IssueKey {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		plaintext = base64.RawURLEncoding.EncodeToString(buf)
		keyHash = hashMachineAPIKey(plaintext)
	}
	var d IoTDevice
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mes_iot_devices (serial, label, asset_tag, api_key_hash)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''))
		RETURNING id, serial, COALESCE(label,''), COALESCE(asset_tag,''),
		          api_key_hash IS NOT NULL, is_active, last_seen, COALESCE(last_ip,''), created_at`,
		serial, strings.TrimSpace(in.Label), assetTag, keyHash,
	).Scan(
		&d.ID, &d.Serial, &d.Label, &d.AssetTag,
		&d.HasAPIKey, &d.IsActive, &d.LastSeen, &d.LastIP, &d.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &CreatedIoTDevice{IoTDevice: d, APIKeyPlaintext: plaintext}, nil
}

func (s *Store) ListIoTDevices(ctx context.Context) ([]IoTDevice, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, serial, COALESCE(label,''), COALESCE(asset_tag,''),
		       api_key_hash IS NOT NULL, is_active, last_seen, COALESCE(last_ip,''), created_at
		FROM mes_iot_devices ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IoTDevice
	for rows.Next() {
		var d IoTDevice
		if err := rows.Scan(
			&d.ID, &d.Serial, &d.Label, &d.AssetTag,
			&d.HasAPIKey, &d.IsActive, &d.LastSeen, &d.LastIP, &d.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetIoTDevice(ctx context.Context, id int64) (*IoTDevice, error) {
	var d IoTDevice
	err := s.pool.QueryRow(ctx, `
		SELECT id, serial, COALESCE(label,''), COALESCE(asset_tag,''),
		       api_key_hash IS NOT NULL, is_active, last_seen, COALESCE(last_ip,''), created_at
		FROM mes_iot_devices WHERE id = $1`, id,
	).Scan(
		&d.ID, &d.Serial, &d.Label, &d.AssetTag,
		&d.HasAPIKey, &d.IsActive, &d.LastSeen, &d.LastIP, &d.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrIoTDeviceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) UpdateIoTDevice(ctx context.Context, id int64, in UpdateIoTDeviceInput) (*IoTDevice, error) {
	cur, err := s.GetIoTDevice(ctx, id)
	if err != nil {
		return nil, err
	}
	label, asset, active := cur.Label, cur.AssetTag, cur.IsActive
	if in.Label != nil {
		label = strings.TrimSpace(*in.Label)
	}
	if in.AssetTag != nil {
		asset = strings.TrimSpace(*in.AssetTag)
		if err := s.ensureAssetTag(ctx, asset); err != nil {
			return nil, err
		}
	}
	if in.IsActive != nil {
		active = *in.IsActive
	}
	var d IoTDevice
	err = s.pool.QueryRow(ctx, `
		UPDATE mes_iot_devices
		SET label = NULLIF($1, ''), asset_tag = NULLIF($2, ''), is_active = $3
		WHERE id = $4
		RETURNING id, serial, COALESCE(label,''), COALESCE(asset_tag,''),
		          api_key_hash IS NOT NULL, is_active, last_seen, COALESCE(last_ip,''), created_at`,
		label, asset, active, id,
	).Scan(
		&d.ID, &d.Serial, &d.Label, &d.AssetTag,
		&d.HasAPIKey, &d.IsActive, &d.LastSeen, &d.LastIP, &d.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) RotateIoTDeviceAPIKey(ctx context.Context, id int64) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	plaintext := base64.RawURLEncoding.EncodeToString(buf)
	tag, err := s.pool.Exec(ctx,
		`UPDATE mes_iot_devices SET api_key_hash = $1 WHERE id = $2`,
		hashMachineAPIKey(plaintext), id)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrIoTDeviceNotFound
	}
	return plaintext, nil
}
