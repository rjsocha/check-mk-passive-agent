package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("data"), 0o640); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	fresh := write(t, dir, "fresh.example.com", time.Hour)
	stale := write(t, dir, "stale.example.com", 8*24*time.Hour)
	tmpNew := write(t, dir, ".check:host:abc", 10*time.Minute)
	tmpOld := write(t, dir, ".check:host:def", 2*time.Hour)

	cleanup(dir, 7*24*time.Hour)

	for _, c := range []struct {
		path string
		want bool
	}{
		{fresh, true},
		{stale, false},
		{tmpNew, true},
		{tmpOld, false},
	} {
		if exists(c.path) != c.want {
			t.Fatalf("%s: exists %v, want %v", filepath.Base(c.path), exists(c.path), c.want)
		}
	}
}

func TestCleanupDisabled(t *testing.T) {
	dir := t.TempDir()
	stale := write(t, dir, "stale.example.com", 30*24*time.Hour)
	cleanup(dir, 0)
	if !exists(stale) {
		t.Fatal("removed a file although retention is disabled")
	}
}

func TestRetentionSetting(t *testing.T) {
	t.Setenv("CMK_PASSIVE_RETENTION", "48h")
	s, err := loadSettings()
	if err != nil || s.Retention != 48*time.Hour {
		t.Fatalf("retention %v, err %v", s.Retention, err)
	}
	t.Setenv("CMK_PASSIVE_RETENTION", "nope")
	if _, err := loadSettings(); err == nil {
		t.Fatal("accepted an invalid duration")
	}
}
