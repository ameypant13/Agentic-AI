package certs

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Worker rescans on an interval and keeps the latest inventory on disk, so
// pages and the Copilot read a stored result instead of scanning.
type Worker struct {
	src      Sources
	path     string
	interval time.Duration

	mu      sync.RWMutex
	inv     *Inventory
	running bool
	trigger chan struct{}
}

func NewWorker(src Sources, dir string, interval time.Duration) (*Worker, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	w := &Worker{src: src, path: filepath.Join(dir, "inventory.json"), interval: interval, trigger: make(chan struct{}, 1)}
	if raw, err := os.ReadFile(w.path); err == nil {
		var inv Inventory
		if json.Unmarshal(raw, &inv) == nil {
			w.inv = &inv
		}
	}
	return w, nil
}

// Start scans now unless the stored inventory is fresh, then every interval.
func (w *Worker) Start(ctx context.Context) {
	go func() {
		first := time.Duration(0)
		if inv := w.Inventory(); inv != nil && inv.Version == ScannerVersion {
			if age := time.Since(inv.ScannedAt); age < w.interval {
				first = w.interval - age
			}
		}
		timer := time.NewTimer(first)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			case <-w.trigger:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			w.RunOnce(ctx)
			timer.Reset(w.interval)
		}
	}()
}

// Trigger asks for a scan now; it is a no-op while one is queued.
func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

func (w *Worker) RunOnce(ctx context.Context) *Inventory {
	w.mu.Lock()
	w.running = true
	w.mu.Unlock()
	sctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	inv := Scan(sctx, w.src)
	cancel()
	w.mu.Lock()
	w.inv, w.running = inv, false
	w.mu.Unlock()
	if err := w.save(inv); err != nil {
		log.Printf("certs: save inventory: %v", err)
	}
	log.Printf("certs: scanned %d certificates in %dms (%d source errors)", len(inv.Items), inv.DurationMs, len(inv.Errors))
	return inv
}

func (w *Worker) save(inv *Inventory) error {
	raw, err := json.Marshal(inv)
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// Inventory returns the latest scan, or nil before the first one.
func (w *Worker) Inventory() *Inventory {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.inv
}

func (w *Worker) Running() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

func (w *Worker) Interval() time.Duration { return w.interval }

// Item returns the stored item with key, or nil.
func (w *Worker) Item(key string) *Item {
	inv := w.Inventory()
	if inv == nil {
		return nil
	}
	for i := range inv.Items {
		if inv.Items[i].Key == key {
			it := inv.Items[i]
			return &it
		}
	}
	return nil
}
