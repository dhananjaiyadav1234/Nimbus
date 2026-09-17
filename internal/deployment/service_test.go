package deployment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestService(t *testing.T) (*Service, *fakeRepository) {
	t.Helper()
	repo := newFakeRepository()
	svc, err := NewService(repo)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, repo
}

func TestNewServiceRejectsNilRepository(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Error("NewService(nil) succeeded, want an error")
	}
}

func TestServiceCreateSuccess(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	d, err := svc.Create(ctx, validCreateInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.ID == uuid.Nil {
		t.Error("Create did not assign an ID")
	}
	if d.Name != "web" {
		t.Errorf("Name = %q, want %q", d.Name, "web")
	}
	if d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
		t.Error("CreatedAt/UpdatedAt were not set")
	}
	if repo.createCalls != 1 {
		t.Errorf("createCalls = %d, want 1", repo.createCalls)
	}
}

func TestServiceCreateRejectsInvalidInput(t *testing.T) {
	svc, repo := newTestService(t)
	in := validCreateInput()
	in.Name = ""

	_, err := svc.Create(context.Background(), in)
	if err == nil {
		t.Fatal("Create succeeded, want a validation error")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("error = %v, want a *ValidationError", err)
	}
	if repo.createCalls != 0 {
		t.Errorf("createCalls = %d, want 0 — invalid input must never reach the repository", repo.createCalls)
	}
}

func TestServiceCreateDuplicateNameReturnsErrAlreadyExists(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, validCreateInput()); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := svc.Create(ctx, validCreateInput())
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("second Create error = %v, want ErrAlreadyExists", err)
	}
}

func TestServiceGetSuccess(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, validCreateInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get returned ID %s, want %s", got.ID, created.ID)
	}
}

func TestServiceGetUnknownReturnsErrNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestServiceGetByNameSuccess(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, validCreateInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.GetByName(ctx, "web")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GetByName returned ID %s, want %s", got.ID, created.ID)
	}
}

func TestServiceListOrdersByName(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	for _, name := range []string{"charlie", "alice", "bob"} {
		in := validCreateInput()
		in.Name = name
		if _, err := svc.Create(ctx, in); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	deployments, err := svc.List(ctx)
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

func TestServiceDeleteSuccess(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, validCreateInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: error = %v, want ErrNotFound", err)
	}
}

func TestServiceDeleteUnknownReturnsErrNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	err := svc.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// repositoryFailure is a Repository whose every method fails, for testing
// that Service surfaces (rather than swallows) unexpected repository
// errors.
type repositoryFailure struct{ err error }

func (r repositoryFailure) Create(context.Context, Deployment) (Deployment, error) {
	return Deployment{}, r.err
}
func (r repositoryFailure) GetByID(context.Context, uuid.UUID) (Deployment, error) {
	return Deployment{}, r.err
}
func (r repositoryFailure) GetByName(context.Context, string) (Deployment, error) {
	return Deployment{}, r.err
}
func (r repositoryFailure) List(context.Context) ([]Deployment, error) { return nil, r.err }
func (r repositoryFailure) Delete(context.Context, uuid.UUID) error    { return r.err }

func TestServicePropagatesUnexpectedRepositoryErrors(t *testing.T) {
	failure := errors.New("connection refused")
	svc, err := NewService(repositoryFailure{err: failure})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	if _, err := svc.Create(ctx, validCreateInput()); !errors.Is(err, failure) {
		t.Errorf("Create error = %v, want it to wrap %v", err, failure)
	}
	if _, err := svc.Get(ctx, uuid.New()); !errors.Is(err, failure) {
		t.Errorf("Get error = %v, want it to wrap %v", err, failure)
	}
	if _, err := svc.List(ctx); !errors.Is(err, failure) {
		t.Errorf("List error = %v, want it to wrap %v", err, failure)
	}
	if err := svc.Delete(ctx, uuid.New()); !errors.Is(err, failure) {
		t.Errorf("Delete error = %v, want it to wrap %v", err, failure)
	}
}

func TestServiceConcurrentCreateOfSameNameOnlyOneSucceeds(t *testing.T) {
	svc, repo := newTestService(t)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	successes := make(chan uuid.UUID, n)
	conflicts := 0
	var mu sync.Mutex

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := svc.Create(ctx, validCreateInput())
			if err == nil {
				successes <- d.ID
				return
			}
			if errors.Is(err, ErrAlreadyExists) {
				mu.Lock()
				conflicts++
				mu.Unlock()
				return
			}
			t.Errorf("unexpected error: %v", err)
		}()
	}
	wg.Wait()
	close(successes)

	var ids []uuid.UUID
	for id := range successes {
		ids = append(ids, id)
	}
	if len(ids) != 1 {
		t.Errorf("%d Create calls succeeded, want exactly 1 (fake repository serializes with its own mutex)", len(ids))
	}
	if conflicts != n-1 {
		t.Errorf("conflicts = %d, want %d", conflicts, n-1)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("repository has %d rows, want 1", len(all))
	}
}

func TestServiceUsesInjectedClockForTimestamps(t *testing.T) {
	svc, _ := newTestService(t)
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	d, err := svc.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !d.CreatedAt.Equal(fixed) || !d.UpdatedAt.Equal(fixed) {
		t.Errorf("CreatedAt=%v UpdatedAt=%v, want both %v", d.CreatedAt, d.UpdatedAt, fixed)
	}
}
