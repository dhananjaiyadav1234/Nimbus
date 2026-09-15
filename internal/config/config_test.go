package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// isolateEnv removes every NIMBUS_* variable for the duration of the test so
// results do not depend on the developer's shell, restoring them afterwards.
func isolateEnv(t *testing.T) {
	t.Helper()

	saved := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "NIMBUS_") {
			saved[key] = value
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unsetting %s: %v", key, err)
			}
		}
	}

	t.Cleanup(func() {
		for key, value := range saved {
			os.Setenv(key, value)
		}
	})
}

func TestLoadAppliesDevelopmentDefaults(t *testing.T) {
	isolateEnv(t)
	// The password is the one setting with no default.
	t.Setenv("NIMBUS_DATABASE_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Env", cfg.Env, EnvDevelopment},
		{"LogLevel", cfg.LogLevel, "info"},
		{"Server.Host", cfg.Server.Host, "0.0.0.0"},
		{"Server.Port", cfg.Server.Port, 8080},
		{"Server.ShutdownTimeout", cfg.Server.ShutdownTimeout, 15 * time.Second},
		{"Database.Host", cfg.Database.Host, "localhost"},
		{"Database.Port", cfg.Database.Port, 5432},
		{"Database.Name", cfg.Database.Name, "nimbus"},
		{"Database.User", cfg.Database.User, "nimbus"},
		{"Database.SSLMode", cfg.Database.SSLMode, "disable"},
		{"Database.ConnectTimeout", cfg.Database.ConnectTimeout, 5 * time.Second},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	if !cfg.IsDevelopment() {
		t.Error("IsDevelopment() = false, want true")
	}
}

func TestLoadReadsEnvironmentOverrides(t *testing.T) {
	isolateEnv(t)
	for key, value := range map[string]string{
		"NIMBUS_ENV":                            EnvProduction,
		"NIMBUS_LOG_LEVEL":                      "debug",
		"NIMBUS_CONTROL_PLANE_HOST":             "127.0.0.1",
		"NIMBUS_CONTROL_PLANE_PORT":             "9090",
		"NIMBUS_CONTROL_PLANE_SHUTDOWN_TIMEOUT": "30s",
		"NIMBUS_DATABASE_HOST":                  "db.internal",
		"NIMBUS_DATABASE_PORT":                  "6543",
		"NIMBUS_DATABASE_NAME":                  "nimbus_prod",
		"NIMBUS_DATABASE_USER":                  "operator",
		"NIMBUS_DATABASE_PASSWORD":              "secret",
		"NIMBUS_DATABASE_SSL_MODE":              "require",
		"NIMBUS_DATABASE_MAX_OPEN_CONNS":        "50",
		"NIMBUS_DATABASE_MAX_IDLE_CONNS":        "10",
		"NIMBUS_DATABASE_CONN_MAX_LIFETIME":     "1h",
		"NIMBUS_DATABASE_CONNECT_TIMEOUT":       "2s",
	} {
		t.Setenv(key, value)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Env != EnvProduction || cfg.IsDevelopment() {
		t.Errorf("Env = %q, IsDevelopment() = %v; want %q and false", cfg.Env, cfg.IsDevelopment(), EnvProduction)
	}
	if got, want := cfg.Server.Address(), "127.0.0.1:9090"; got != want {
		t.Errorf("Server.Address() = %q, want %q", got, want)
	}
	if cfg.Server.ShutdownTimeout != 30*time.Second {
		t.Errorf("Server.ShutdownTimeout = %s, want 30s", cfg.Server.ShutdownTimeout)
	}
	if cfg.Database.Host != "db.internal" || cfg.Database.Port != 6543 {
		t.Errorf("Database host:port = %s:%d, want db.internal:6543", cfg.Database.Host, cfg.Database.Port)
	}
	if cfg.Database.MaxOpenConns != 50 || cfg.Database.MaxIdleConns != 10 {
		t.Errorf("pool sizing = %d/%d, want 50/10", cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	}
	if cfg.Database.ConnMaxLifetime != time.Hour {
		t.Errorf("Database.ConnMaxLifetime = %s, want 1h", cfg.Database.ConnMaxLifetime)
	}
}

func TestLoadRequiresDatabasePassword(t *testing.T) {
	isolateEnv(t)

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded without a password, want an error")
	}
	if !strings.Contains(err.Error(), "NIMBUS_DATABASE_PASSWORD") {
		t.Errorf("error %q does not name the missing variable", err)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		"unknown environment":         {map[string]string{"NIMBUS_ENV": "prod"}, "NIMBUS_ENV"},
		"unknown log level":           {map[string]string{"NIMBUS_LOG_LEVEL": "verbose"}, "NIMBUS_LOG_LEVEL"},
		"non-numeric port":            {map[string]string{"NIMBUS_CONTROL_PLANE_PORT": "http"}, "NIMBUS_CONTROL_PLANE_PORT"},
		"port out of range":           {map[string]string{"NIMBUS_CONTROL_PLANE_PORT": "70000"}, "NIMBUS_CONTROL_PLANE_PORT"},
		"malformed duration":          {map[string]string{"NIMBUS_DATABASE_CONNECT_TIMEOUT": "5 seconds"}, "NIMBUS_DATABASE_CONNECT_TIMEOUT"},
		"unknown ssl mode":            {map[string]string{"NIMBUS_DATABASE_SSL_MODE": "maybe"}, "NIMBUS_DATABASE_SSL_MODE"},
		"idle exceeds open conns":     {map[string]string{"NIMBUS_DATABASE_MAX_OPEN_CONNS": "2", "NIMBUS_DATABASE_MAX_IDLE_CONNS": "5"}, "NIMBUS_DATABASE_MAX_IDLE_CONNS"},
		"negative heartbeat interval": {map[string]string{"NIMBUS_HEARTBEAT_INTERVAL": "-10s"}, "NIMBUS_HEARTBEAT_INTERVAL"},
		"zero failure threshold":      {map[string]string{"NIMBUS_HEARTBEAT_FAILURE_THRESHOLD": "0"}, "NIMBUS_HEARTBEAT_FAILURE_THRESHOLD"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("NIMBUS_DATABASE_PASSWORD", "secret")
			for key, value := range tc.env {
				t.Setenv(key, value)
			}

			_, err := Load()
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %s", err, tc.want)
			}
		})
	}
}

// Every problem should surface in one run rather than one per fix cycle.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	isolateEnv(t)
	t.Setenv("NIMBUS_ENV", "nowhere")
	t.Setenv("NIMBUS_LOG_LEVEL", "loud")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded, want an error")
	}

	for _, want := range []string{"NIMBUS_ENV", "NIMBUS_LOG_LEVEL", "NIMBUS_DATABASE_PASSWORD"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestClusterConfigDefaultsAndTimeout(t *testing.T) {
	isolateEnv(t)
	t.Setenv("NIMBUS_DATABASE_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Cluster.HeartbeatInterval != 10*time.Second {
		t.Errorf("HeartbeatInterval = %s, want 10s", cfg.Cluster.HeartbeatInterval)
	}
	if cfg.Cluster.FailureThreshold != 3 {
		t.Errorf("FailureThreshold = %d, want 3", cfg.Cluster.FailureThreshold)
	}
	if want := 30 * time.Second; cfg.Cluster.Timeout() != want {
		t.Errorf("Timeout() = %s, want %s (interval * threshold)", cfg.Cluster.Timeout(), want)
	}
}

func TestClusterConfigReadsOverrides(t *testing.T) {
	isolateEnv(t)
	t.Setenv("NIMBUS_DATABASE_PASSWORD", "secret")
	t.Setenv("NIMBUS_HEARTBEAT_INTERVAL", "5s")
	t.Setenv("NIMBUS_HEARTBEAT_FAILURE_THRESHOLD", "5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Cluster.HeartbeatInterval != 5*time.Second {
		t.Errorf("HeartbeatInterval = %s, want 5s", cfg.Cluster.HeartbeatInterval)
	}
	if cfg.Cluster.FailureThreshold != 5 {
		t.Errorf("FailureThreshold = %d, want 5", cfg.Cluster.FailureThreshold)
	}
	if want := 25 * time.Second; cfg.Cluster.Timeout() != want {
		t.Errorf("Timeout() = %s, want %s", cfg.Cluster.Timeout(), want)
	}
}

func TestLoadNodeAgentAppliesDefaults(t *testing.T) {
	isolateEnv(t)

	cfg, err := LoadNodeAgent()
	if err != nil {
		t.Fatalf("LoadNodeAgent: %v", err)
	}

	if cfg.ControlPlaneURL != "http://localhost:8080" {
		t.Errorf("ControlPlaneURL = %q, want default", cfg.ControlPlaneURL)
	}
	if cfg.NodeDataDir != "./.nimbus" {
		t.Errorf("NodeDataDir = %q, want default", cfg.NodeDataDir)
	}
	if cfg.NodeName != "" {
		t.Errorf("NodeName = %q, want empty by default (falls back to os.Hostname)", cfg.NodeName)
	}
	if cfg.HeartbeatInterval != 10*time.Second {
		t.Errorf("HeartbeatInterval = %s, want 10s", cfg.HeartbeatInterval)
	}
	if cfg.AgentVersion != "0.1.0" {
		t.Errorf("AgentVersion = %q, want default", cfg.AgentVersion)
	}
}

func TestLoadNodeAgentReadsOverrides(t *testing.T) {
	isolateEnv(t)
	t.Setenv("NIMBUS_CONTROL_PLANE_URL", "http://control-plane.internal:9090")
	t.Setenv("NIMBUS_NODE_DATA_DIR", "/tmp/nimbus-node-a")
	t.Setenv("NIMBUS_NODE_NAME", "node-a")
	t.Setenv("NIMBUS_HEARTBEAT_INTERVAL", "3s")
	t.Setenv("NIMBUS_AGENT_VERSION", "9.9.9")

	cfg, err := LoadNodeAgent()
	if err != nil {
		t.Fatalf("LoadNodeAgent: %v", err)
	}

	if cfg.ControlPlaneURL != "http://control-plane.internal:9090" {
		t.Errorf("ControlPlaneURL = %q, want override", cfg.ControlPlaneURL)
	}
	if cfg.NodeDataDir != "/tmp/nimbus-node-a" {
		t.Errorf("NodeDataDir = %q, want override", cfg.NodeDataDir)
	}
	if cfg.NodeName != "node-a" {
		t.Errorf("NodeName = %q, want override", cfg.NodeName)
	}
	if cfg.HeartbeatInterval != 3*time.Second {
		t.Errorf("HeartbeatInterval = %s, want 3s", cfg.HeartbeatInterval)
	}
	if cfg.AgentVersion != "9.9.9" {
		t.Errorf("AgentVersion = %q, want override", cfg.AgentVersion)
	}
}

func TestLoadNodeAgentRejectsInvalidValues(t *testing.T) {
	tests := map[string]struct {
		env  map[string]string
		want string
	}{
		// Note: NIMBUS_CONTROL_PLANE_URL and NIMBUS_NODE_DATA_DIR both have
		// non-empty defaults, and loader.str treats an explicitly-empty
		// environment variable the same as an unset one (falls back to the
		// default) — see internal/config/config.go's `loader.str`. So an
		// empty-string override can never actually reach validate() as "",
		// and there is no case for it here.
		"malformed control plane url": {map[string]string{"NIMBUS_CONTROL_PLANE_URL": "://not-a-url"}, "NIMBUS_CONTROL_PLANE_URL"},
		"unsupported scheme":          {map[string]string{"NIMBUS_CONTROL_PLANE_URL": "ftp://host"}, "NIMBUS_CONTROL_PLANE_URL"},
		"missing host":                {map[string]string{"NIMBUS_CONTROL_PLANE_URL": "http://"}, "NIMBUS_CONTROL_PLANE_URL"},
		"negative heartbeat interval": {map[string]string{"NIMBUS_HEARTBEAT_INTERVAL": "-1s"}, "NIMBUS_HEARTBEAT_INTERVAL"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			isolateEnv(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}

			_, err := LoadNodeAgent()
			if err == nil {
				t.Fatal("LoadNodeAgent succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %s", err, tc.want)
			}
		})
	}
}

func TestLoadNodeAgentEmptyDataDirUsesDefaultWhenUnset(t *testing.T) {
	// Guards against a subtle regression: an *unset* NIMBUS_NODE_DATA_DIR
	// must fall back to the default, not be treated as the empty-string
	// validation failure exercised above.
	isolateEnv(t)

	cfg, err := LoadNodeAgent()
	if err != nil {
		t.Fatalf("LoadNodeAgent: %v", err)
	}
	if cfg.NodeDataDir == "" {
		t.Error("NodeDataDir is empty, want the default")
	}
}
