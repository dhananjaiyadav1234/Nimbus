package deployment

import (
	"errors"
	"strings"
	"testing"
)

func validCreateInput() CreateInput {
	return CreateInput{
		Name:        "web",
		Image:       "nginx:latest",
		Replicas:    2,
		CPU:         1,
		MemoryBytes: 512 * 1024 * 1024,
	}
}

func TestCreateInputValidateAccepsValidInput(t *testing.T) {
	if err := validCreateInput().Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestCreateInputValidateRejectsInvalidFields(t *testing.T) {
	tests := map[string]func(*CreateInput){
		"empty name":              func(in *CreateInput) { in.Name = "" },
		"name too long":           func(in *CreateInput) { in.Name = strings.Repeat("a", MaxNameLength+1) },
		"name with uppercase":     func(in *CreateInput) { in.Name = "Web" },
		"name with underscore":    func(in *CreateInput) { in.Name = "web_app" },
		"name starting with dash": func(in *CreateInput) { in.Name = "-web" },
		"name ending with dash":   func(in *CreateInput) { in.Name = "web-" },
		"name with space":         func(in *CreateInput) { in.Name = "web app" },
		"empty image":             func(in *CreateInput) { in.Image = "" },
		"image with whitespace":   func(in *CreateInput) { in.Image = "nginx: latest" },
		"image too long":          func(in *CreateInput) { in.Image = strings.Repeat("a", MaxImageLength+1) },
		"negative replicas":       func(in *CreateInput) { in.Replicas = -1 },
		"replicas above max":      func(in *CreateInput) { in.Replicas = MaxReplicas + 1 },
		"zero cpu":                func(in *CreateInput) { in.CPU = 0 },
		"negative cpu":            func(in *CreateInput) { in.CPU = -1 },
		"cpu above max":           func(in *CreateInput) { in.CPU = MaxCPU + 1 },
		"zero memory":             func(in *CreateInput) { in.MemoryBytes = 0 },
		"negative memory":         func(in *CreateInput) { in.MemoryBytes = -1 },
		"memory above max":        func(in *CreateInput) { in.MemoryBytes = MaxMemoryBytes + 1 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			in := validCreateInput()
			mutate(&in)

			err := in.Validate()
			if err == nil {
				t.Fatal("Validate succeeded, want a validation error")
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Errorf("error = %v, want a *ValidationError", err)
			}
		})
	}
}

func TestCreateInputValidateAcceptsBoundaryValues(t *testing.T) {
	tests := map[string]func(*CreateInput){
		"replicas = 0":            func(in *CreateInput) { in.Replicas = 0 },
		"replicas = MaxReplicas":  func(in *CreateInput) { in.Replicas = MaxReplicas },
		"cpu = MaxCPU":            func(in *CreateInput) { in.CPU = MaxCPU },
		"memory = MaxMemoryBytes": func(in *CreateInput) { in.MemoryBytes = MaxMemoryBytes },
		"name at MaxNameLength":   func(in *CreateInput) { in.Name = strings.Repeat("a", MaxNameLength) },
		"single-char name":        func(in *CreateInput) { in.Name = "a" },
		"name with interior dash": func(in *CreateInput) { in.Name = "web-app-1" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			in := validCreateInput()
			mutate(&in)
			if err := in.Validate(); err != nil {
				t.Errorf("Validate: %v, want success at this boundary", err)
			}
		})
	}
}

func TestCreateInputValidateReportsEveryProblemAtOnce(t *testing.T) {
	in := CreateInput{Name: "", Image: "", Replicas: -1, CPU: 0, MemoryBytes: 0}

	err := in.Validate()
	if err == nil {
		t.Fatal("Validate succeeded, want an error")
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error = %v, want a *ValidationError", err)
	}
	if len(verr.Problems) < 5 {
		t.Errorf("Problems has %d entries, want at least 5 (one per invalid field): %v", len(verr.Problems), verr.Problems)
	}
}
