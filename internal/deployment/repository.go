package deployment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolation is PostgreSQL's SQLSTATE for a UNIQUE constraint failure.
const uniqueViolation = "23505"

// Repository persists deployments in PostgreSQL. It is the only place in
// Nimbus that issues SQL against the `deployments` table; Service depends
// on this interface rather than a concrete type so it can be exercised in
// tests without a database — mirroring internal/cluster.Repository.
type Repository interface {
	// Create inserts a new deployment. Returns ErrAlreadyExists if the name
	// is already taken — enforced by PostgreSQL's UNIQUE constraint, so this
	// is correct even under concurrent creation, not an application-level
	// race.
	Create(ctx context.Context, d Deployment) (Deployment, error)

	// GetByID returns ErrNotFound if id does not exist.
	GetByID(ctx context.Context, id uuid.UUID) (Deployment, error)

	// GetByName returns ErrNotFound if name does not exist.
	GetByName(ctx context.Context, name string) (Deployment, error)

	// List returns every known deployment ordered by name ascending.
	List(ctx context.Context) ([]Deployment, error)

	// Delete removes a deployment. Returns ErrNotFound if id does not
	// exist. It removes only the desired-state row — see model.go and
	// docs/workloads.md for why that is all "delete" means in Phase 2.1.
	Delete(ctx context.Context, id uuid.UUID) error
}

// postgresRepository is the PostgreSQL-backed Repository.
type postgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository builds a Repository backed by db. The `deployments`
// table must already exist — see internal/database's migration runner.
func NewPostgresRepository(db *sql.DB) Repository {
	return &postgresRepository{db: db}
}

const deploymentColumns = `id, name, image, replicas, cpu, memory_bytes, created_at, updated_at`

func (r *postgresRepository) Create(ctx context.Context, d Deployment) (Deployment, error) {
	const query = `
		INSERT INTO deployments (id, name, image, replicas, cpu, memory_bytes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + deploymentColumns

	row := r.db.QueryRowContext(ctx, query,
		d.ID, d.Name, d.Image, d.Replicas, d.CPU, d.MemoryBytes, d.CreatedAt, d.UpdatedAt,
	)

	saved, err := scanDeployment(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Deployment{}, ErrAlreadyExists
		}
		return Deployment{}, fmt.Errorf("deployment: creating %q: %w", d.Name, err)
	}
	return saved, nil
}

func (r *postgresRepository) GetByID(ctx context.Context, id uuid.UUID) (Deployment, error) {
	const query = `SELECT ` + deploymentColumns + ` FROM deployments WHERE id = $1`

	d, err := scanDeployment(r.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("deployment: getting %s: %w", id, err)
	}
	return d, nil
}

func (r *postgresRepository) GetByName(ctx context.Context, name string) (Deployment, error) {
	const query = `SELECT ` + deploymentColumns + ` FROM deployments WHERE name = $1`

	d, err := scanDeployment(r.db.QueryRowContext(ctx, query, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("deployment: getting %q: %w", name, err)
	}
	return d, nil
}

func (r *postgresRepository) List(ctx context.Context) ([]Deployment, error) {
	const query = `SELECT ` + deploymentColumns + ` FROM deployments ORDER BY name ASC, id ASC`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("deployment: listing: %w", err)
	}
	defer rows.Close()

	deployments := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("deployment: listing: %w", err)
		}
		deployments = append(deployments, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("deployment: listing: %w", err)
	}
	return deployments, nil
}

func (r *postgresRepository) Delete(ctx context.Context, id uuid.UUID) error {
	const query = `DELETE FROM deployments WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("deployment: deleting %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("deployment: deleting %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDeployment(row rowScanner) (Deployment, error) {
	var d Deployment
	err := row.Scan(
		&d.ID, &d.Name, &d.Image, &d.Replicas, &d.CPU, &d.MemoryBytes, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return Deployment{}, err
	}
	return d, nil
}
