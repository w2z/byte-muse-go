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

func TestBTPreferenceIncludesJavDBAndNyaa(t *testing.T) {
	for _, site := range []string{"JavDB", "Nyaa BT"} {
		selected, _ := selectResource([]torrentsearch.Resource{{Kind: "pt", Site: "Private", InfoHash: "a"}, {Kind: "bt", Site: site, InfoHash: "z"}}, domain.SubscriptionModeStrict, nil, "site", "BT")
		if selected == nil || selected.Kind != "bt" {
			t.Fatalf("site=%s selected=%+v", site, selected)
		}
	}
}

// TestExcludeVR 在严格与预下载模式中均排除 VR，且保留普通资源。
func TestExcludeVR(t *testing.T) {
	for _, mode := range []domain.SubscriptionMode{domain.SubscriptionModeStrict, domain.SubscriptionModePreload} {
		items := []torrentsearch.Resource{{Title: "TEST-vr-001", InfoHash: "a", Seeders: 100}, {Title: "TEST-002", InfoHash: "b", Seeders: 1}}
		selected, passed := selectResource(items, mode, map[string]any{"exclude_vr": true}, "seeders", "")
		if selected == nil || selected.InfoHash != "b" || !passed {
			t.Fatalf("selected=%+v passed=%v", selected, passed)
		}
		selected, _ = selectResource(items[:1], mode, map[string]any{"exclude_vr": true}, "seeders", "")
		if selected != nil {
			t.Fatalf("VR was not excluded: %+v", selected)
		}
		selected, _ = selectResource(items, mode, map[string]any{"exclude_vr": false}, "seeders", "")
		if selected == nil || selected.InfoHash != "a" {
			t.Fatal("disabled exclusion altered selection")
		}
	}
}

func TestExcludeVRUsesMediaCodeWhenResourceTitleOmitsIt(t *testing.T) {
	selected, _ := selectResource([]torrentsearch.Resource{{Title: "video", InfoHash: "a"}}, domain.SubscriptionModePreload, map[string]any{"exclude_vr": true}, "seeders", "", "TEST-VR-001")
	if selected != nil {
		t.Fatal("VR media code bypassed exclusion")
	}
}
