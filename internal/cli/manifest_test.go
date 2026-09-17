package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "deployment.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing manifest file: %v", err)
	}
	return path
}

func TestParseManifestFileValid(t *testing.T) {
	path := writeManifestFile(t, `
apiVersion: nimbus/v1
kind: Deployment
metadata:
  name: web
spec:
  image: nginx:latest
  replicas: 2
  resources:
    cpu: 1
    memory: 512Mi
`)

	m, err := ParseManifestFile(path)
	if err != nil {
		t.Fatalf("ParseManifestFile: %v", err)
	}
	if m.Metadata.Name != "web" {
		t.Errorf("Name = %q, want %q", m.Metadata.Name, "web")
	}
	if m.Spec.Image != "nginx:latest" {
		t.Errorf("Image = %q, want %q", m.Spec.Image, "nginx:latest")
	}
	if m.Spec.Replicas != 2 {
		t.Errorf("Replicas = %d, want 2", m.Spec.Replicas)
	}
	if m.Spec.Resources == nil || m.Spec.Resources.CPU != 1 || m.Spec.Resources.Memory != "512Mi" {
		t.Errorf("Resources = %+v, want CPU=1 Memory=512Mi", m.Spec.Resources)
	}
}

func TestParseManifestFileMissingFile(t *testing.T) {
	_, err := ParseManifestFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("ParseManifestFile succeeded on a missing file, want an error")
	}
}

func TestParseManifestFileMalformedYAML(t *testing.T) {
	path := writeManifestFile(t, "not: valid: yaml: [structure")
	_, err := ParseManifestFile(path)
	if err == nil {
		t.Fatal("ParseManifestFile succeeded on malformed YAML, want an error")
	}
}

func TestParseManifestFileWrongAPIVersion(t *testing.T) {
	path := writeManifestFile(t, `
apiVersion: nimbus/v2
kind: Deployment
metadata:
  name: web
spec:
  image: nginx:latest
  replicas: 1
  resources: {cpu: 1, memory: 512Mi}
`)
	_, err := ParseManifestFile(path)
	if err == nil {
		t.Fatal("ParseManifestFile succeeded with a wrong apiVersion, want an error")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("error = %q, want it to mention apiVersion", err)
	}
}

func TestParseManifestFileWrongKind(t *testing.T) {
	path := writeManifestFile(t, `
apiVersion: nimbus/v1
kind: Pod
metadata:
  name: web
spec:
  image: nginx:latest
  replicas: 1
  resources: {cpu: 1, memory: 512Mi}
`)
	_, err := ParseManifestFile(path)
	if err == nil {
		t.Fatal("ParseManifestFile succeeded with a wrong kind, want an error")
	}
}

func TestParseManifestFileMissingName(t *testing.T) {
	path := writeManifestFile(t, `
apiVersion: nimbus/v1
kind: Deployment
metadata:
  name: ""
spec:
  image: nginx:latest
  replicas: 1
  resources: {cpu: 1, memory: 512Mi}
`)
	_, err := ParseManifestFile(path)
	if err == nil {
		t.Fatal("ParseManifestFile succeeded with an empty name, want an error")
	}
}

func TestParseManifestFileMissingResources(t *testing.T) {
	path := writeManifestFile(t, `
apiVersion: nimbus/v1
kind: Deployment
metadata:
  name: web
spec:
  image: nginx:latest
  replicas: 1
`)
	_, err := ParseManifestFile(path)
	if err == nil {
		t.Fatal("ParseManifestFile succeeded with no resources block, want an error")
	}
}
