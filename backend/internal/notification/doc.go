// Package notification turns outbox events into in-app notifications (FR-NOT).
//
// The River outbox.deliver worker calls Consumer.Deliver. Inserts use
// ON CONFLICT (user_id, outbox_event_id) so at-least-once delivery never
// duplicates a row. User-facing copy lives in templates.go, separate from
// routing. Critical failures also go through Sender (dev logs today).
package notification
