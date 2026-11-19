package core

import (
	"context"
	"time"
)

// AsyncTasks owns durable identities and snapshots of opt-in plugin tasks.
// No query through this port calls upstream or selects another account.
// A task is registered only by ExecutionRecords.CommitExecution, from the
// TaskSubmission its plugin's Execute returned (ExecutionCommit.Task).
type AsyncTasks interface {
	BeginTask(context.Context, TaskIntent) (string, error)
	ReceiveTask(context.Context, string, int, []byte, string) error
	FindTask(context.Context, TaskQuery) (*TaskSnapshot, error)
}

type TaskIntent struct {
	RequestID, PluginKey, Kind, Model, Protocol string
	UserID, APIKeyID, GroupID, AccountID        int64
}

// TaskRegistration is the task of an ExecutionCommit; the commit sets Record.
type TaskRegistration struct {
	PublicID, UpstreamID, Kind string
	Snapshot                   []byte
	IDPaths                    []string
	NextCheckAfter, Deadline   time.Duration
	Record                     *UsageRecord
}

type TaskQuery struct {
	ID, PluginKey, Kind, SubmitProtocol string
	UserID, GroupID                     int64
	IDPaths                             []string
}

type TaskSnapshot struct {
	FailureCode, FailureReason                            string
	PublicID, UpstreamID, Model, State, ObservationStatus string
	Body                                                  []byte
	IDPaths                                               []string
}
