package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
	"slices"
	"testing"
)

// TestDownloadWaitingStatesRemainControllable verifies resume need not start transferring immediately.
func TestDownloadWaitingStatesRemainControllable(t *testing.T) {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, status := range []string{"downloading", "queued", "stalled", "checking", "metadata", "moving"} {
		t.Run(status, func(t *testing.T) {
			task := domain.DownloadTask{InfoHash: &hash, TransferStatus: &status}
			if actions := downloadActions(task, []string{"stop", "delete"}); !slices.Equal(actions, []string{"stop", "delete"}) {
				t.Fatalf("actions=%v", actions)
			}
			for _, action := range []string{"resume", "retry"} {
				if !controlConfirmed(action, &ports.TransferState{Status: status}) {
					t.Fatalf("%s not confirmed for %s", action, status)
				}
			}
		})
	}
	for _, status := range []string{"paused", "stopped", "failed", "unknown"} {
		if controlConfirmed("resume", &ports.TransferState{Status: status}) {
			t.Fatalf("resume falsely confirmed for %s", status)
		}
	}
}
