package notification

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/notification/notificationdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

const (
	defaultListLimit = 50
	maxListLimit     = 100
)

// Service reads and marks notifications for the customer API.
type Service struct {
	pool *pgxpool.Pool
	q    *notificationdb.Queries
}

// NewService builds a notification read service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: notificationdb.New(pool)}
}

// Notification is the customer-facing inbox row.
type Notification struct {
	ID        uuid.UUID       `json:"id"`
	Topic     string          `json:"topic"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Data      json.RawMessage `json:"data"`
	ReadAt    *time.Time      `json:"read_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// ListResult is GET /v1/notifications.
type ListResult struct {
	Items       []Notification `json:"items"`
	UnreadCount int64          `json:"unread_count"`
}

// List returns the caller's newest notifications.
func (s *Service) List(ctx context.Context, userID uuid.UUID, limitRaw string) (ListResult, error) {
	if s.pool == nil {
		return ListResult{}, httpx.Internal("Notification store is not configured.")
	}
	if userID == uuid.Nil {
		return ListResult{}, unauthorized()
	}
	limit, err := parseLimit(limitRaw)
	if err != nil {
		return ListResult{}, err
	}
	rows, err := s.q.ListNotifications(ctx, notificationdb.ListNotificationsParams{
		UserID: userID,
		Limit:  limit,
	})
	if err != nil {
		return ListResult{}, err
	}
	unread, err := s.q.CountUnread(ctx, userID)
	if err != nil {
		return ListResult{}, err
	}
	items := make([]Notification, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapNotification(row))
	}
	return ListResult{Items: items, UnreadCount: unread}, nil
}

// MarkRead marks one notification owned by the caller.
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) (Notification, error) {
	if s.pool == nil {
		return Notification{}, httpx.Internal("Notification store is not configured.")
	}
	if userID == uuid.Nil {
		return Notification{}, unauthorized()
	}
	if id == uuid.Nil {
		return Notification{}, httpx.ValidationFailed("Notification id is required.")
	}
	row, err := s.q.MarkRead(ctx, notificationdb.MarkReadParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Notification{}, notFound()
		}
		return Notification{}, err
	}
	return mapNotification(row), nil
}

func mapNotification(row notificationdb.Notification) Notification {
	data := row.Data
	if len(data) == 0 {
		data = []byte("{}")
	}
	return Notification{
		ID:        row.ID,
		Topic:     row.Topic,
		Title:     row.Title,
		Body:      row.Body,
		Data:      data,
		ReadAt:    row.ReadAt,
		CreatedAt: row.CreatedAt,
	}
}

func parseLimit(raw string) (int32, error) {
	if raw == "" {
		return defaultListLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxListLimit {
		return 0, httpx.ValidationFailed("limit must be between 1 and 100.")
	}
	return int32(n), nil
}

func unauthorized() *httpx.Error {
	return httpx.New(http.StatusUnauthorized, "unauthorized", "Unauthorized", "Missing or invalid Authorization header.")
}

func notFound() *httpx.Error {
	return httpx.New(http.StatusNotFound, "not_found", "Not Found", "Notification not found.")
}
