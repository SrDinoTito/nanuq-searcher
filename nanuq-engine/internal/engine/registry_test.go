package engine

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"nanuq-engine/internal/config"
	"nanuq-engine/internal/result"
)

// fakeEngine is a minimal Engine implementation used to exercise the registry
// (TASK-003 validation: register + instantiate with a mock factory).
type fakeEngine struct {
	name     string
	shortcut string
}

func (f *fakeEngine) Name() string { return f.name }
func (f *fakeEngine) Shortcut() string {
	return f.shortcut
}
func (f *fakeEngine) Categories() []string { return []string{"general"} }
func (f *fakeEngine) NeedsInit() bool      { return false }
func (f *fakeEngine) Setup(ctx context.Context, cfg *config.EngineConfig) error {
	return nil
}
func (f *fakeEngine) Init(ctx context.Context) error { return nil }
func (f *fakeEngine) Request(query string, params *RequestParams) error {
	return nil
}
func (f *fakeEngine) Response(resp *http.Response) ([]*result.RawResult, error) {
	return nil, nil
}

// Compile-time assertion: fakeEngine satisfies the Engine contract. This also
// pins the contract's shape so a signature change fails the build (REQ-005).
var _ Engine = (*fakeEngine)(nil)

// fakeFactory builds a fakeEngine named after the YAML entry and records the
// pointer it received, so tests can assert the config was passed through.
func fakeFactory(seen **config.EngineConfig) Factory {
	return func(cfg *config.EngineConfig) (Engine, error) {
		*seen = cfg
		return &fakeEngine{name: cfg.Name, shortcut: cfg.Shortcut}, nil
	}
}

func TestRegistryRegisterAndHas(t *testing.T) {
	r := New()
	if r.Has("duckduckgo") {
		t.Fatal("Has returned true for an unregistered module")
	}
	if err := r.Register("duckduckgo", fakeFactory(nil)); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}
	if !r.Has("duckduckgo") {
		t.Fatal("Has returned false after Register")
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	r := New()
	if err := r.Register("ddg", fakeFactory(nil)); err != nil {
		t.Fatalf("first Register: unexpected error: %v", err)
	}
	if err := r.Register("ddg", fakeFactory(nil)); err == nil {
		t.Fatal("Register of a duplicate module succeeded, want error")
	}
}

func TestRegistryRegisterInvalidArguments(t *testing.T) {
	r := New()
	if err := r.Register("", fakeFactory(nil)); err == nil {
		t.Fatal("Register with empty module name succeeded, want error")
	}
	if err := r.Register("nil-factory", nil); err == nil {
		t.Fatal("Register with nil factory succeeded, want error")
	}
}

func TestRegistryInstantiate(t *testing.T) {
	r := New()
	var seen *config.EngineConfig
	if err := r.Register("duckduckgo", fakeFactory(&seen)); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}

	cfg := config.EngineConfig{Name: "duckduckgo_extra", Engine: "duckduckgo", Shortcut: "ddg"}
	eng, err := r.Instantiate(cfg)
	if err != nil {
		t.Fatalf("Instantiate: unexpected error: %v", err)
	}
	if eng.Name() != "duckduckgo_extra" {
		t.Errorf("instance Name = %q, want %q", eng.Name(), "duckduckgo_extra")
	}
	if eng.Shortcut() != "ddg" {
		t.Errorf("instance Shortcut = %q, want %q", eng.Shortcut(), "ddg")
	}
	if seen == nil {
		t.Fatal("factory did not receive the EngineConfig")
	}
	if !reflect.DeepEqual(seen, &cfg) {
		t.Errorf("factory received cfg %+v, want %+v", seen, &cfg)
	}
}

func TestRegistryInstantiateNotFound(t *testing.T) {
	r := New()
	if err := r.Register("duckduckgo", fakeFactory(nil)); err != nil {
		t.Fatalf("Register: unexpected error: %v", err)
	}

	cfg := config.EngineConfig{Name: "ghost", Engine: "ghost_engine"}
	_, err := r.Instantiate(cfg)
	if !errors.Is(err, ErrEngineNotFound) {
		t.Fatalf("Instantiate error = %v, want errors.Is(err, ErrEngineNotFound)", err)
	}
}

func TestRegistryNamesSorted(t *testing.T) {
	r := New()
	for _, name := range []string{"zed", "alpha", "mike", "beta"} {
		if err := r.Register(name, fakeFactory(nil)); err != nil {
			t.Fatalf("Register(%q): unexpected error: %v", name, err)
		}
	}
	got := r.Names()
	want := []string{"alpha", "beta", "mike", "zed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}
