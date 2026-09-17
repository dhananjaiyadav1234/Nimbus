package deployment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/database/dbtest"
)

// These are integration tests: they run against a real PostgreSQL database
// (see internal/database/dbtest) to exercise the actual SQL — in
// particular the UNIQUE constraint on name, which is what actually
// guarantees idempotent creation, not application logic. They skip cleanly
// if PostgreSQL is not reachable.

func newTestRepository(t *testing.T) Repository {
	t.Helper()
	db := dbtest.Open(t)
	return NewPostgresRepository(db)
}

func testDeployment(name string) Deployment {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return Deployment{
		ID:          uuid.New(),
		Name:        name,
		Image:       "nginx:latest",
		Replicas:    2,
		CPU:         1,
		MemoryBytes: 512 * 1024 * 1024,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestPostgresRepositoryCreateAndGetByID(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	d := testDeployment("web")

	saved, err := repo.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if saved.ID != d.ID {
		t.Errorf("ID = %s, want %s", saved.ID, d.ID)
	}

	fetched, err := repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if fetched.Name != "web" || fetched.Image != "nginx:latest" || fetched.Replicas != 2 {
		t.Errorf("fetched = %+v, want Name=web Image=nginx:latest Replicas=2", fetched)
	}
	if !fetched.CreatedAt.Equal(d.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", fetched.CreatedAt, d.CreatedAt)
	}
}

func TestPostgresRepositoryGetByName(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	d := testDeployment("api")

	if _, err := repo.Create(ctx, d); err != nil {
		t.Fatalf("Create: %v", err)
	}

	fetched, err := repo.GetByName(ctx, "api")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if fetched.ID != d.ID {
		t.Errorf("ID = %s, want %s", fetched.ID, d.ID)
	}
}

func TestPostgresRepositoryGetByIDUnknownReturnsErrNotFound(t *testing.T) {
	repo := newTestRepository(t)
	_, err := repo.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryGetByNameUnknownReturnsErrNotFound(t *testing.T) {
	repo := newTestRepository(t)
	_, err := repo.GetByName(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryCreateDuplicateNameReturnsErrAlreadyExists(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	if _, err := repo.Create(ctx, testDeployment("web")); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := repo.Create(ctx, testDeployment("web")) // different ID, same name
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("second Create error = %v, want ErrAlreadyExists", err)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List returned %d rows, want 1 (no duplicate row from the rejected create)", len(all))
	}
}

func TestPostgresRepositoryListOrdersByName(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	for _, name := range []string{"charlie", "alice", "bob"} {
		if _, err := repo.Create(ctx, testDeployment(name)); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	deployments, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(deployments) != 3 {
		t.Fatalf("List returned %d deployments, want 3", len(deployments))
	}
	want := []string{"alice", "bob", "charlie"}
	for i, name := range want {
		if deployments[i].Name != name {
			t.Errorf("deployments[%d].Name = %q, want %q", i, deployments[i].Name, name)
		}
	}
}

func TestPostgresRepositoryDelete(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	d := testDeployment("web")

	if _, err := repo.Create(ctx, d); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Delete(ctx, d.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID after Delete: error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryDeleteUnknownReturnsErrNotFound(t *testing.T) {
	repo := newTestRepository(t)
	err := repo.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryRejectsInvalidResourceValues(t *testing.T) {
	tests := map[string]func(*Deployment){
		"negative replicas": func(d *Deployment) { d.Replicas = -1 },
		"zero cpu":          func(d *Deployment) { d.CPU = 0 },
		"negative cpu":      func(d *Deployment) { d.CPU = -1 },
		"zero memory":       func(d *Deployment) { d.MemoryBytes = 0 },
		"negative memory":   func(d *Deployment) { d.MemoryBytes = -1 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepository(t)
			d := testDeployment("bad-" + name)
			mutate(&d)

			_, err := repo.Create(context.Background(), d)
			if err == nil {
				t.Fatal("Create succeeded, want the CHECK constraint to reject it")
			}
		})
	}
}

// The mandatory concurrency test: PostgreSQL's UNIQUE constraint, not
// application logic, must be what makes creation idempotent under real
// concurrent load.
func TestPostgresRepositoryConcurrentCreateOfSameNameExactlyOneSucceeds(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Create(ctx, testDeployment("concurrent-web"))
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyExists):
			conflicts++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}

	if successes != 1 {
		t.Errorf("successes = %d, want exactly 1", successes)
	}
	if conflicts != n-1 {
		t.Errorf("conflicts = %d, want %d", conflicts, n-1)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("database has %d rows named concurrent-web, want exactly 1 — the UNIQUE constraint must enforce this even under concurrency", len(all))
	}
}
