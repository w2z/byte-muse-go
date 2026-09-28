package application

import (
	"sort"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/torrentsearch"
)

// selectResource applies the legacy strict/preload semantics to normalized resources.
// The boolean reports whether the selected candidate meets the subscription filter.
func selectResource(items []torrentsearch.Resource, mode domain.SubscriptionMode, filter map[string]any, sortOrder, mainSite string) (*torrentsearch.Resource, bool) {
	seen := make(map[string]bool, len(items))
	candidates := make([]torrentsearch.Resource, 0, len(items))
	for _, item := range items {
		key := strings.ToLower(item.InfoHash)
		if key == "" {
			key = item.URI
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		candidates = append(candidates, item)
	}
	matching := make([]torrentsearch.Resource, 0, len(candidates))
	for _, item := range candidates {
		if resourceMatches(item, filter) {
			matching = append(matching, item)
		}
	}
	passed := len(matching) > 0
	if passed {
		candidates = matching
	} else if mode == domain.SubscriptionModeStrict {
		return nil, false
	}
	if len(candidates) == 0 {
		return nil, false
	}
	keys := strings.Split(sortOrder, ",")
	if sortOrder == "" {
		keys = []string{"seeders"}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		for _, key := range keys {
			switch strings.TrimSpace(key) {
			case "seeders":
				if a.Seeders != b.Seeders {
					return a.Seeders > b.Seeders
				}
			case "chinese":
				if a.Chinese != b.Chinese {
					return a.Chinese
				}
			case "uhd":
				if a.UHD != b.UHD {
					return a.UHD
				}
			case "!uhd":
				if a.UHD != b.UHD {
					return !a.UHD
				}
			case "uc":
				if a.UC != b.UC {
					return a.UC
				}
			case "!uc":
				if a.UC != b.UC {
					return !a.UC
				}
			case "free":
				if a.Free != b.Free {
					return a.Free
				}
			case "site":
				if mainSite != "" && mainSite != "ALL" && (a.Site == mainSite) != (b.Site == mainSite) {
					return a.Site == mainSite
				}
			}
		}
		return a.InfoHash < b.InfoHash
	})
	return &candidates[0], passed
}

func resourceMatches(item torrentsearch.Resource, filter map[string]any) bool {
	for key, raw := range filter {
		switch key {
		case "max_size", "min_size":
			if raw == nil || raw == "" {
				continue
			}
			n, ok := numericFilter(raw)
			if !ok {
				return false
			}
			if key == "max_size" && item.SizeMB > n || key == "min_size" && item.SizeMB < n {
				return false
			}
		case "only_chinese":
			if raw == true && !item.Chinese {
				return false
			}
		case "only_uc":
			if raw == true && !item.UC {
				return false
			}
		case "only_uhd":
			if raw == true && !item.UHD {
				return false
			}
		case "exclude_uc":
			if raw == true && item.UC {
				return false
			}
		case "exclude_uhd":
			if raw == true && item.UHD {
				return false
			}
		case "only_free":
			if raw == true && !item.Free {
				return false
			}
		}
	}
	return true
}

func numericFilter(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}
