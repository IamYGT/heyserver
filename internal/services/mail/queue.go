package mail

import (
	"fmt"
	"time"
)

// queueDomainEntry holds per-domain delivery status inside a queued message.
type queueDomainEntry struct {
	Domain    string    `json:"domain"`
	Status    string    `json:"status"`
	NextRetry time.Time `json:"next_retry,omitempty"`
	Retries   int       `json:"retries"`
}

// queueItem is the raw Stalwart queue message shape.
type queueItem struct {
	ID         string             `json:"id"`
	ReturnPath string             `json:"return_path"`
	Domains    []queueDomainEntry `json:"domains"`
	Recipients []string           `json:"recipients"`
	Created    time.Time          `json:"created"`
}

// queueResponse is the envelope returned by GET /api/queue/messages.
type queueResponse struct {
	Items []queueItem `json:"items"`
	Total int         `json:"total"`
}

// GetQueueStatus returns all messages currently in the outbound queue (up to 100).
func (s *Service) GetQueueStatus() ([]QueueMessage, error) {
	return s.ListQueueMessages(100)
}

// ListQueueMessages returns up to limit messages from the outbound queue.
// Pass limit <= 0 to use the server default (Stalwart returns up to 100 by default).
func (s *Service) ListQueueMessages(limit int) ([]QueueMessage, error) {
	url := "/api/queue/messages"
	if limit > 0 {
		url = fmt.Sprintf("%s?limit=%d", url, limit)
	}

	var qr queueResponse
	if err := s.get(url, &qr); err != nil {
		return nil, fmt.Errorf("list queue messages: %w", err)
	}

	msgs := make([]QueueMessage, 0, len(qr.Items))
	for _, item := range qr.Items {
		qm := QueueMessage{
			ID:         item.ID,
			Sender:     item.ReturnPath,
			Recipients: item.Recipients,
			CreatedAt:  item.Created,
		}
		if len(item.Domains) > 0 {
			qm.NextRetry = item.Domains[0].NextRetry
			qm.Retries = item.Domains[0].Retries
		}
		msgs = append(msgs, qm)
	}
	return msgs, nil
}

// RetryMessage immediately reschedules the given message for delivery.
// Stalwart reschedules via PATCH /api/queue/messages/{id} (no body required).
func (s *Service) RetryMessage(id string) error {
	if id == "" {
		return fmt.Errorf("message id is required")
	}
	// An empty PATCH body tells Stalwart to retry now.
	if err := s.patch("/api/queue/messages/"+id, nil, nil); err != nil {
		return fmt.Errorf("retry message %s: %w", id, err)
	}
	return nil
}

// DeleteMessage cancels and removes the given message from the outbound queue.
func (s *Service) DeleteMessage(id string) error {
	if id == "" {
		return fmt.Errorf("message id is required")
	}
	if err := s.delete("/api/queue/messages/" + id); err != nil {
		return fmt.Errorf("delete queue message %s: %w", id, err)
	}
	return nil
}
