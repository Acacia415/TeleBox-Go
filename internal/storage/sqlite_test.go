package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDBLifecycleAndKV(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "nested", "telebox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "core", "answer", []byte("42")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "core", "answer")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "42" {
		t.Fatalf("Get() = %q, want 42", got)
	}

	got[0] = 'x'
	again, err := store.Get(ctx, "core", "answer")
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != "42" {
		t.Fatal("Get() returned aliased storage memory")
	}

	if err := store.Delete(ctx, "core", "answer"); err != nil {
		t.Fatal(err)
	}
	_, err = store.Get(ctx, "core", "answer")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestPluginStatesAreUpsertedAndSorted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "telebox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, state := range []PluginState{
		{Name: "zeta", Enabled: false},
		{Name: "alpha", Enabled: true, ConfigJSON: `{"mode":"safe"}`},
		{Name: "zeta", Enabled: true},
	} {
		if err := store.SetPluginState(ctx, state); err != nil {
			t.Fatal(err)
		}
	}

	states, err := store.PluginStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(states))
	for _, state := range states {
		names = append(names, state.Name)
	}
	if !reflect.DeepEqual(names, []string{"alpha", "zeta"}) {
		t.Fatalf("plugin names = %v", names)
	}
	if !states[1].Enabled {
		t.Fatal("zeta state was not updated")
	}
}
