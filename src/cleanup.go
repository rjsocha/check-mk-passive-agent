package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const tempPrefix = ".check:"
const tempRetention = time.Hour

func cleanup(storage string, retention time.Duration) {
	entries, err := os.ReadDir(storage)
	if err != nil {
		log.Printf("cleanup: %v", err)
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		limit := retention
		if strings.HasPrefix(entry.Name(), tempPrefix) {
			limit = tempRetention
		}
		if limit <= 0 || age <= limit {
			continue
		}
		if err := os.Remove(filepath.Join(storage, entry.Name())); err != nil {
			log.Printf("cleanup: %v", err)
			continue
		}
		log.Printf("cleanup: removed %s, last update %s ago", entry.Name(), age.Truncate(time.Minute))
	}
}

func cleanupLoop(storage string, retention time.Duration, interval time.Duration, done <-chan struct{}) {
	cleanup(storage, retention)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cleanup(storage, retention)
		case <-done:
			return
		}
	}
}
