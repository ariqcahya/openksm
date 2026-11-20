package attachment

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxAttachmentBytes = 5 * 1024 * 1024 // 5 MB

var (
	ErrInvalidFile = errors.New("invalid attachment: unsupported MIME type or size")
	allowedMIME    = map[string]bool{
		"image/jpeg":      true,
		"image/png":       true,
		"image/webp":      true,
		"application/pdf": true,
	}
)

type Record struct {
	ID           string `json:"id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	BucketName   string `json:"bucket_name"`
	ObjectKey    string `json:"object_key"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
}

// ValidateContent enforces NFR-SEC-06: size limit and magic-byte MIME verification.
func ValidateContent(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxAttachmentBytes {
		return "", fmt.Errorf("%w: file size must be 1..%d bytes", ErrInvalidFile, MaxAttachmentBytes)
	}
	detected := http.DetectContentType(data)
	if !allowedMIME[detected] {
		return "", fmt.Errorf("%w: detected type %q is not allowed", ErrInvalidFile, detected)
	}
	return detected, nil
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) SaveMetadata(ctx context.Context, actorUserID, resourceType, resourceID, bucketName, objectKey, contentType string, sizeBytes int64) (*Record, error) {
	var r Record
	err := s.pool.QueryRow(ctx, `
		INSERT INTO object_attachments (resource_type, resource_id, bucket_name, object_key, content_type, size_bytes, uploaded_by)
		VALUES ($1, $2::uuid, $3, $4, $5, $6, $7::uuid)
		RETURNING id::text, resource_type, resource_id::text, bucket_name, object_key, content_type, size_bytes
	`, resourceType, resourceID, bucketName, objectKey, contentType, sizeBytes, actorUserID).Scan(
		&r.ID, &r.ResourceType, &r.ResourceID, &r.BucketName, &r.ObjectKey, &r.ContentType, &r.SizeBytes,
	)
	if err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'object_attachment', $2::uuid, 'attachment:upload', 'succeeded', $3::uuid)
	`, actorUserID, r.ID, uuid.NewString())
	return &r, nil
}

func (s *Service) ListByResource(ctx context.Context, resourceType, resourceID string) ([]Record, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, resource_type, resource_id::text, bucket_name, object_key, content_type, size_bytes
		FROM object_attachments
		WHERE resource_type = $1 AND resource_id = $2::uuid
		ORDER BY created_at DESC
	`, resourceType, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.ResourceType, &r.ResourceID, &r.BucketName, &r.ObjectKey, &r.ContentType, &r.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
