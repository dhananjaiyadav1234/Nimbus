package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RegisterResult is what a successful registration hands back to the caller:
// the canonical, persisted node plus the heartbeat cadence it should honour.
type RegisterResult struct {
	Node              Node
	HeartbeatInterval time.Duration
}

// HeartbeatResult is what a successful heartbeat hands back to the caller.
// Recovered is true exactly when the node had been StatusNotReady
// immediately before this heartbeat — i.e. this call is what brought it back
// to StatusReady — so a caller can log that specific transition without
// logging every ordinary Ready-stays-Ready heartbeat.
type HeartbeatResult struct {
	Node      Node
	Recovered bool
}

// Service enforces Nimbus's cluster-membership rules on top of a Repository.
// It is the only place those rules live — HTTP handlers translate wire
// formats to and from these methods, and never touch SQL or Repository
// directly, so a future consumer (for instance a Phase 2 scheduler) can
// depend on Service without coupling itself to the HTTP layer.
type Service struct {
	repo Repository

	heartbeatInterval time.Duration
	failureThreshold  int

	// now stands in for time.Now so tests can control the Control Plane's
	// notion of "the current time" without sleeping. It always defaults to
	// the real clock.
	now func() time.Time
}

// NewService builds a Service. heartbeatInterval and failureThreshold must
// both be positive; a node is considered stale once
// failureThreshold*heartbeatInterval has elapsed since its last heartbeat.
func NewService(repo Repository, heartbeatInterval time.Duration, failureThreshold int) (*Service, error) {
	if repo == nil {
		return nil, errors.New("cluster: repository is required")
	}
	if heartbeatInterval <= 0 {
		return nil, errors.New("cluster: heartbeat interval must be greater than zero")
	}
	if failureThreshold < 1 {
		return nil, errors.New("cluster: failure threshold must be at least 1")
	}

	return &Service{
		repo:              repo,
		heartbeatInterval: heartbeatInterval,
		failureThreshold:  failureThreshold,
		now:               time.Now,
	}, nil
}

// HeartbeatInterval is the cadence agents are told to heartbeat at.
func (s *Service) HeartbeatInterval() time.Duration { return s.heartbeatInterval }

// HeartbeatTimeout is the point past which a node with no heartbeat is
// considered stale: failureThreshold missed intervals, not one.
func (s *Service) HeartbeatTimeout() time.Duration {
	return s.heartbeatInterval * time.Duration(s.failureThreshold)
}

// Register creates a node the Control Plane has never seen, or updates one
// it has, preserving that node's identity and original registration time.
//
// A successful call is itself proof the node can reach the Control Plane —
// equivalent, for membership purposes, to an initial heartbeat — so the
// node is persisted directly as StatusReady with LastHeartbeatAt set to the
// registration time, rather than sitting in StatusRegistering waiting for a
// separate first heartbeat. See docs/cluster-membership.md for the full
// rationale.
func (s *Service) Register(ctx context.Context, in RegisterInput) (RegisterResult, error) {
	if err := in.Validate(); err != nil {
		return RegisterResult{}, err
	}

	id := in.NodeID
	if id == uuid.Nil {
		id = uuid.New()
	}

	now := s.now()
	node := Node{
		ID:                  id,
		Hostname:            in.Hostname,
		Status:              StatusReady,
		OS:                  in.OS,
		Architecture:        in.Architecture,
		CPUCapacity:         in.CPUCapacity,
		MemoryCapacityBytes: in.MemoryCapacityBytes,
		AgentVersion:        in.AgentVersion,
		LastHeartbeatAt:     &now,
		RegisteredAt:        now, // ignored by the repository if id already exists
		UpdatedAt:           now,
	}

	saved, err := s.repo.Upsert(ctx, node)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("cluster: registering node: %w", err)
	}

	return RegisterResult{Node: saved, HeartbeatInterval: s.heartbeatInterval}, nil
}

// Heartbeat records that id is alive as of now, moving it to StatusReady if
// it had been marked StatusNotReady. It returns ErrNotFound if id has never
// been registered — heartbeats never create a node.
func (s *Service) Heartbeat(ctx context.Context, id uuid.UUID) (HeartbeatResult, error) {
	node, previous, err := s.repo.RecordHeartbeat(ctx, id, s.now())
	if errors.Is(err, ErrNotFound) {
		return HeartbeatResult{}, ErrNotFound
	}
	if err != nil {
		return HeartbeatResult{}, fmt.Errorf("cluster: recording heartbeat: %w", err)
	}
	return HeartbeatResult{Node: node, Recovered: previous == StatusNotReady}, nil
}

// Get returns a single node, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Node, error) {
	node, err := s.repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("cluster: getting node: %w", err)
	}
	return node, nil
}

// List returns every known node, ordered by hostname ascending. It always
// queries PostgreSQL directly — see Repository.List — so it reflects
// membership accurately even immediately after a Control Plane restart.
func (s *Service) List(ctx context.Context) ([]Node, error) {
	nodes, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("cluster: listing nodes: %w", err)
	}
	return nodes, nil
}

// DetectStaleNodes moves every StatusReady node whose last heartbeat is
// older than HeartbeatTimeout, as of now, to StatusNotReady, and returns
// exactly the nodes that transitioned (for the caller to log). now is a
// parameter rather than read from the system clock internally so the
// membership monitor's timing logic can be tested with fixed timestamps
// instead of sleeping in tests.
func (s *Service) DetectStaleNodes(ctx context.Context, now time.Time) ([]Node, error) {
	cutoff := now.Add(-s.HeartbeatTimeout())

	stale, err := s.repo.MarkStale(ctx, cutoff, now)
	if err != nil {
		return nil, fmt.Errorf("cluster: detecting stale nodes: %w", err)
	}
	return stale, nil
}
