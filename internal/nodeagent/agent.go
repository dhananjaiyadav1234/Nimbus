package nodeagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/dhananjaiyadav1234/Nimbus/internal/clusterapi"
	"github.com/dhananjaiyadav1234/Nimbus/internal/config"
)

// defaultRegistrationBackoffBase and defaultRegistrationBackoffMax bound how
// the agent retries a Control Plane it cannot currently reach — for both the
// initial registration and any later re-registration after an unknown-node
// 404. They are Agent fields, not constants, purely so tests can shrink them
// and exercise several retries in milliseconds instead of tens of seconds.
const (
	defaultRegistrationBackoffBase = time.Second
	defaultRegistrationBackoffMax  = 30 * time.Second
)

// ControlPlaneClient is exactly the Control Plane behaviour Agent depends
// on. *Client implements it; tests substitute a stub so Agent's retry and
// lifecycle logic can be verified without a real HTTP server.
type ControlPlaneClient interface {
	Register(ctx context.Context, req clusterapi.RegisterRequest) (clusterapi.RegisterResponse, error)
	Heartbeat(ctx context.Context, nodeID uuid.UUID, req clusterapi.HeartbeatRequest) (clusterapi.HeartbeatResponse, error)
}

// Agent runs a single Nimbus node's lifecycle: load or create a local
// identity, register with the Control Plane (retrying transient failures),
// then heartbeat on a ticker until its context is cancelled.
type Agent struct {
	cfg    config.NodeAgentConfig
	logger *slog.Logger
	client ControlPlaneClient

	// machineInfo is discoverMachineInfo by default; tests override it so
	// Agent's own logic can be verified without depending on the real host's
	// hardware.
	machineInfo func(nameOverride string) (MachineInfo, error)

	// heartbeatInterval starts at the configured default and is replaced by
	// the Control Plane's own value from the registration response — the
	// Control Plane is the source of truth for cluster behaviour, including
	// how often it expects to hear from this node.
	heartbeatInterval time.Duration

	// registrationBackoffBase/Max default to the package defaults; tests
	// shrink them to exercise several retries quickly.
	registrationBackoffBase time.Duration
	registrationBackoffMax  time.Duration
}

// New builds an Agent. A nil client defaults to NewClient(cfg.ControlPlaneURL).
func New(cfg config.NodeAgentConfig, logger *slog.Logger, client ControlPlaneClient) *Agent {
	if client == nil {
		client = NewClient(cfg.ControlPlaneURL)
	}
	return &Agent{
		cfg:                     cfg,
		logger:                  logger,
		client:                  client,
		machineInfo:             discoverMachineInfo,
		heartbeatInterval:       cfg.HeartbeatInterval,
		registrationBackoffBase: defaultRegistrationBackoffBase,
		registrationBackoffMax:  defaultRegistrationBackoffMax,
	}
}

// Run loads or creates the agent's local identity, registers with the
// Control Plane, and then heartbeats until ctx is cancelled. It returns nil
// on a graceful shutdown (ctx cancelled) and a non-nil error only for a
// genuine startup failure — a corrupt identity file or undiscoverable
// machine info — that occurs before the agent could ever register.
func (a *Agent) Run(ctx context.Context) error {
	id, err := loadOrCreateIdentity(a.cfg.NodeDataDir)
	if err != nil {
		return err
	}
	a.logger.Info("node agent started",
		slog.String("node_id", id.NodeID.String()),
		slog.String("data_dir", a.cfg.NodeDataDir),
		slog.String("control_plane_url", a.cfg.ControlPlaneURL),
	)

	if err := a.registerWithRetry(ctx, id.NodeID); err != nil {
		if ctx.Err() != nil {
			a.logger.Info("node agent shutting down before initial registration completed")
			return nil
		}
		return err
	}

	a.heartbeatLoop(ctx, id.NodeID)
	a.logger.Info("node agent stopped")
	return nil
}

// registerWithRetry registers nodeID, retrying with bounded exponential
// backoff while the Control Plane is unreachable or erroring, until either
// registration succeeds or ctx is cancelled. A non-nil return therefore
// always means ctx was cancelled — every other failure is retried
// internally rather than surfaced.
func (a *Agent) registerWithRetry(ctx context.Context, nodeID uuid.UUID) error {
	info, err := a.machineInfo(a.cfg.NodeName)
	if err != nil {
		return fmt.Errorf("nodeagent: discovering machine info: %w", err)
	}

	req := clusterapi.RegisterRequest{
		NodeID:              nodeID.String(),
		Hostname:            info.Hostname,
		OS:                  info.OS,
		Architecture:        info.Architecture,
		CPUCapacity:         info.CPUCapacity,
		MemoryCapacityBytes: info.MemoryCapacityBytes,
		AgentVersion:        a.cfg.AgentVersion,
	}

	retry := newBackoff(a.registrationBackoffBase, a.registrationBackoffMax)
	for {
		resp, err := a.client.Register(ctx, req)
		if err == nil {
			a.logger.Info("node agent registered",
				slog.String("node_id", resp.NodeID),
				slog.String("status", resp.Status),
			)
			if resp.HeartbeatIntervalSeconds > 0 {
				a.heartbeatInterval = time.Duration(resp.HeartbeatIntervalSeconds) * time.Second
			}
			return nil
		}

		delay := retry.next()
		a.logger.Warn("registration failed, retrying",
			slog.String("error", err.Error()),
			slog.Duration("retry_in", delay),
		)
		if sleepErr := sleep(ctx, delay); sleepErr != nil {
			return sleepErr
		}
	}
}

// heartbeatLoop sends a heartbeat every heartbeatInterval until ctx is
// cancelled. An ordinary heartbeat failure is logged and left for the next
// tick — it is not retried immediately and does not trigger
// re-registration, per docs/cluster-membership.md. Only ErrNodeNotFound
// (the Control Plane's 404, meaning its membership record for this node is
// gone — most likely the database was reset) triggers re-registration,
// since that is the one failure a later heartbeat cannot recover from on
// its own.
func (a *Agent) heartbeatLoop(ctx context.Context, nodeID uuid.UUID) {
	ticker := time.NewTicker(a.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			now := time.Now().UTC()
			_, err := a.client.Heartbeat(ctx, nodeID, clusterapi.HeartbeatRequest{Timestamp: &now})
			if err == nil {
				continue
			}

			if errors.Is(err, ErrNodeNotFound) {
				a.logger.Warn("control plane does not recognise this node; re-registering",
					slog.String("node_id", nodeID.String()),
				)
				if regErr := a.registerWithRetry(ctx, nodeID); regErr != nil {
					return // ctx was cancelled during re-registration's retry wait
				}
				ticker.Reset(a.heartbeatInterval) // may have changed on re-registration
				continue
			}

			a.logger.Warn("heartbeat failed, will retry next interval",
				slog.String("error", err.Error()),
				slog.String("node_id", nodeID.String()),
			)
		}
	}
}
