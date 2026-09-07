// Package taskqueue defines the async-job port.
//
// Adapters: in-process Go channels (personal), NATS (enterprise).
// Used for business-registration change polling, AI inference jobs and
// import jobs — handlers are written once against this interface.
package taskqueue

import "context"

// Task is one unit of work. Payload is adapter-opaque bytes (JSON).
type Task struct {
	Type    string
	Payload []byte
}

// Queue is the async port. Enqueue is non-blocking; Consume runs handler
// for each delivered task until the context is canceled.
type Queue interface {
	Enqueue(ctx context.Context, t Task) error
	Consume(ctx context.Context, handler func(Task) error) error
	Close() error
}
