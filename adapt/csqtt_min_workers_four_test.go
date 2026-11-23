package libs

import "testing"

// TestCSQTTMinWorkersFour prevents the pinned upstream Go controller from
// silently restoring the historical 1-worker lower bound in release builds.
func TestCSQTTMinWorkersFour(t *testing.T) {
	if MinWorkers != 4 {
		t.Fatalf("CSQTT Core must reject fewer than 4 workers; MinWorkers=%d", MinWorkers)
	}
	if MaxWorkers != 127 {
		t.Fatalf("unexpected maximum workers: %d", MaxWorkers)
	}
}
