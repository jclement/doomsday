// Package notify provides the notification system for doomsday.
// It supports command execution and webhook POST notifications,
// triggered on backup success, failure, or warning events.
package notify

import (
	"context"
	"fmt"
)

// Event represents a notification-worthy occurrence during backup operations.
type Event struct {
	Status  string // "success", "failure", "warning"
	Message string // human-readable description
	Config  string // backup config name that triggered the event
}

// Notifier is the interface for sending notifications.
type Notifier interface {
	Send(ctx context.Context, event Event) error
}

// Policy determines when notifications are sent.
type Policy string

const (
	PolicyAlways    Policy = "always"
	PolicyOnFailure Policy = "on_failure"
	PolicyNever     Policy = "never"
)

// ShouldNotify returns true if the given event should trigger a notification
// under the specified policy.
func ShouldNotify(policy Policy, event Event) bool {
	switch policy {
	case PolicyAlways:
		return true
	case PolicyOnFailure:
		return event.Status == "failure" || event.Status == "warning"
	case PolicyNever:
		return false
	default:
		// Unknown policy: fail open by notifying on failures.
		return event.Status == "failure"
	}
}

// Multi wraps multiple notifiers and sends to all of them.
// Returns the first error encountered, but attempts all notifiers.
type Multi struct {
	Notifiers []Notifier
}

// Send dispatches the event to all wrapped notifiers.
// Collects all errors and returns a combined error if any failed.
func (m *Multi) Send(ctx context.Context, event Event) error {
	var errs []error
	for _, n := range m.Notifiers {
		if err := n.Send(ctx, event); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return fmt.Errorf("notify.Multi.Send: %w", errs[0])
	}
	return fmt.Errorf("notify.Multi.Send: %d notifiers failed, first: %w", len(errs), errs[0])
}
