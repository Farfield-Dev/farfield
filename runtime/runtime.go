// Package runtime implements Farfield's immutable object-storage run journal.
// It records execution state; worker scheduling and user-code execution remain
// separate concerns.
package runtime

import "fmt"

const EventSchema = "farfield.runtime.event.v1"

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusWaiting   Status = "waiting"
	StatusSleeping  Status = "sleeping"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusAmbiguous Status = "ambiguous"
)

var transitions = map[Status]map[Status]bool{
	StatusQueued:   {StatusRunning: true, StatusCancelled: true},
	StatusRunning:  {StatusWaiting: true, StatusSleeping: true, StatusCompleted: true, StatusFailed: true, StatusCancelled: true, StatusAmbiguous: true},
	StatusWaiting:  {StatusQueued: true, StatusCancelled: true},
	StatusSleeping: {StatusQueued: true, StatusCancelled: true},
}

func ValidateTransition(from, to Status) error {
	if !transitions[from][to] {
		return fmt.Errorf("invalid runtime transition %q -> %q", from, to)
	}
	return nil
}

func (status Status) Terminal() bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusAmbiguous:
		return true
	default:
		return false
	}
}
