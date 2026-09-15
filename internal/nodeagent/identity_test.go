package nodeagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestLoadOrCreateIdentityCreatesNewIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "node-data")

	id, err := loadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity: %v", err)
	}
	if id.NodeID == uuid.Nil {
		t.Error("NodeID is the nil UUID, want a generated one")
	}

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("data directory was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, identityFileName)); err != nil {
		t.Errorf("identity file was not created: %v", err)
	}
}

func TestLoadOrCreateIdentityIsStableAcrossReload(t *testing.T) {
	dir := t.TempDir()

	first, err := loadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("first loadOrCreateIdentity: %v", err)
	}

	second, err := loadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("second loadOrCreateIdentity: %v", err)
	}

	if first.NodeID != second.NodeID {
		t.Errorf("NodeID changed across reload: %s -> %s", first.NodeID, second.NodeID)
	}
}

func TestLoadOrCreateIdentityTwoDataDirsAreIndependentNodes(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "node-a")
	dirB := filepath.Join(t.TempDir(), "node-b")

	a, err := loadOrCreateIdentity(dirA)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity(a): %v", err)
	}
	b, err := loadOrCreateIdentity(dirB)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity(b): %v", err)
	}

	if a.NodeID == b.NodeID {
		t.Error("two different data directories produced the same node ID, want independent identities")
	}
}

func TestLoadOrCreateIdentityRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, identityFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("writing corrupt identity file: %v", err)
	}

	_, err := loadOrCreateIdentity(dir)
	if err == nil {
		t.Fatal("loadOrCreateIdentity succeeded on a corrupt file, want an error")
	}
}

func TestLoadOrCreateIdentityRejectsEmptyNodeID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, identityFileName), []byte(`{"node_id":"00000000-0000-0000-0000-000000000000"}`), 0o644); err != nil {
		t.Fatalf("writing identity file: %v", err)
	}

	_, err := loadOrCreateIdentity(dir)
	if err == nil {
		t.Fatal("loadOrCreateIdentity succeeded with a nil UUID, want an error")
	}
}
