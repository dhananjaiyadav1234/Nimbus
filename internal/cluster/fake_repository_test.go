package cluster

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// fakeRepository is an in-memory Repository used by unit tests so Service
// and Monitor behaviour can be verified without a PostgreSQL server. Its
// semantics deliberately mirror postgresRepository (RegisteredAt preserved
// on conflict, RecordHeartbeat 404s on an unknown ID, MarkStale only ever
// touches StatusReady rows whose heartbeat is older than the cutoff) so unit
// tests exercise the same contract the integration tests hold the real
// implementation to.
type fakeRepository struct {
	mu    sync.Mutex
	nodes map[uuid.UUID]Node

	// upsertCalls counts Upsert invocations, for tests that assert against
	// double-writes (e.g. duplicate registration must not grow the store).
	upsertCalls int
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{nodes: map[uuid.UUID]Node{}}
}

func (f *fakeRepository) Upsert(_ context.Context, node Node) (Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.upsertCalls++
	if existing, ok := f.nodes[node.ID]; ok {
		node.RegisteredAt = existing.RegisteredAt
	}
	f.nodes[node.ID] = node
	return node, nil
}

func (f *fakeRepository) Get(_ context.Context, id uuid.UUID) (Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	node, ok := f.nodes[id]
	if !ok {
		return Node{}, ErrNotFound
	}
	return node, nil
}

func (f *fakeRepository) List(_ context.Context) ([]Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	nodes := make([]Node, 0, len(f.nodes))
	for _, node := range f.nodes {
		nodes = append(nodes, node)
	}
	sortNodesByHostname(nodes)
	return nodes, nil
}

func (f *fakeRepository) RecordHeartbeat(_ context.Context, id uuid.UUID, receivedAt time.Time) (Node, Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	node, ok := f.nodes[id]
	if !ok {
		return Node{}, "", ErrNotFound
	}
	previous := node.Status
	node.LastHeartbeatAt = &receivedAt
	node.UpdatedAt = receivedAt
	node.Status = StatusReady
	f.nodes[id] = node
	return node, previous, nil
}

func (f *fakeRepository) MarkStale(_ context.Context, cutoff, now time.Time) ([]Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var stale []Node
	for id, node := range f.nodes {
		if node.Status != StatusReady {
			continue
		}
		if node.LastHeartbeatAt == nil || !node.LastHeartbeatAt.Before(cutoff) {
			continue
		}
		node.Status = StatusNotReady
		node.UpdatedAt = now
		f.nodes[id] = node
		stale = append(stale, node)
	}
	sortNodesByHostname(stale)
	return stale, nil
}

func sortNodesByHostname(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Hostname < nodes[j].Hostname })
}
