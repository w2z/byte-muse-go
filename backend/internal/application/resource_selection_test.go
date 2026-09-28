package application

import (
	"testing"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/torrentsearch"
)

func TestStrictSubscriptionWaitsForMatchingCandidate(t *testing.T) {
	items := []torrentsearch.Resource{{Title: "SSIS-001 raw", InfoHash: "a", Seeders: 20}, {Title: "SSIS-001 中文字幕", InfoHash: "b", Seeders: 2, Chinese: true}}
	selected, passed := selectResource(items, domain.SubscriptionModeStrict, map[string]any{"only_chinese": true}, "seeders", "")
	if selected == nil || selected.InfoHash != "b" || !passed {
		t.Fatalf("selected=%#v passed=%v", selected, passed)
	}
	selected, passed = selectResource(items[:1], domain.SubscriptionModeStrict, map[string]any{"only_chinese": true}, "seeders", "")
	if selected != nil || passed {
		t.Fatalf("strict accepted unmatched resource: %#v", selected)
	}
}

func TestPreloadCanSelectUnmatchedCandidateWithoutCompletingSubscription(t *testing.T) {
	items := []torrentsearch.Resource{{Title: "SSIS-001 raw", InfoHash: "a", Seeders: 20}}
	selected, passed := selectResource(items, domain.SubscriptionModePreload, map[string]any{"only_chinese": true}, "seeders", "")
	if selected == nil || selected.InfoHash != "a" || passed {
		t.Fatalf("selected=%#v passed=%v", selected, passed)
	}
}

func TestSelectionDeduplicatesHashAndAppliesSize(t *testing.T) {
	items := []torrentsearch.Resource{{Title: "large", InfoHash: "a", SizeMB: 4096, Seeders: 100}, {Title: "small", InfoHash: "b", SizeMB: 1000, Seeders: 3}, {Title: "duplicate", InfoHash: "b", SizeMB: 1000, Seeders: 50}}
	selected, _ := selectResource(items, domain.SubscriptionModeStrict, map[string]any{"max_size": float64(2048)}, "seeders", "")
	if selected == nil || selected.Title != "small" {
		t.Fatalf("selected=%#v", selected)
	}
}

func TestUnsetSizeFilterDoesNotRejectResources(t *testing.T) {
	items := []torrentsearch.Resource{{Title: "film", InfoHash: "a", SizeMB: 2048, Seeders: 1}}
	selected, passed := selectResource(items, domain.SubscriptionModeStrict, map[string]any{"min_size": nil, "max_size": nil, "only_chinese": false}, "seeders", "")
	if selected == nil || !passed {
		t.Fatalf("unset numeric filters rejected resource: %#v", selected)
	}
}
