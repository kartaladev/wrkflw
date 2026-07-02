package store_test

import (
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
)

// TestNewStoreNilArgs verifies that New rejects nil conn and nil dialect.
func TestNewStoreNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.New(nil, d); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.New(struct{}{}, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}

// TestNewCallLinkStoreNilArgs verifies that NewCallLinkStore rejects nil conn and nil dialect.
func TestNewCallLinkStoreNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.NewCallLinkStore(nil, d); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.NewCallLinkStore(struct{}{}, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}

// TestNewChainLinkStoreNilArgs verifies that NewChainLinkStore rejects nil conn and nil dialect.
func TestNewChainLinkStoreNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.NewChainLinkStore(nil, d); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.NewChainLinkStore(struct{}{}, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}

// TestNewDeduperNilArgs verifies that NewDeduper rejects nil conn and nil dialect.
func TestNewDeduperNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.NewDeduper(nil, d); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.NewDeduper(struct{}{}, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}

// TestNewDefinitionStoreNilArgs verifies that NewDefinitionStore rejects nil conn and nil dialect.
func TestNewDefinitionStoreNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.NewDefinitionStore(nil, d); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.NewDefinitionStore(struct{}{}, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}

// TestNewListerNilArgs verifies that NewLister rejects nil conn and nil dialect.
func TestNewListerNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.NewLister(nil, d); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.NewLister(struct{}{}, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}

// TestNewPrunerNilArgs verifies that NewPruner rejects nil conn and nil dialect.
func TestNewPrunerNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.NewPruner(nil, d); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.NewPruner(struct{}{}, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}

// TestNewTimerStoreNilArgs verifies that NewTimerStore rejects nil conn and nil dialect.
func TestNewTimerStoreNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.NewTimerStore(nil, d); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.NewTimerStore(struct{}{}, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}

// TestNewRelayNilArgs verifies that NewRelay rejects nil conn and nil dialect.
func TestNewRelayNilArgs(t *testing.T) {
	d := dialect.NewSQLite()

	if _, err := store.NewRelay(nil, d, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := store.NewRelay(struct{}{}, nil, nil); !errors.Is(err, store.ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}
