package application

import (
	"context"
	"errors"
	"testing"

	"bytemuse/backend/internal/platform/torrentsearch"
)

type privateMarker struct{ prefix string }

func (p privateMarker) Download(_ context.Context, reference string) ([]byte, string, string, error) {
	return []byte(reference), p.prefix, "https://example.test/torrent", nil
}

func TestPrivateTorrentSourcesRouteByReference(t *testing.T) {
	sources := PrivateTorrentSources{Sources: map[string]PrivateTorrentSource{
		"mteam":    privateMarker{prefix: "mteam-hash"},
		"ptfans":   privateMarker{prefix: "ptfans-hash"},
		"rousipro": privateMarker{prefix: "rousipro-hash"},
	}}
	got, hash, _, err := sources.Download(context.Background(), "ptfans:9432")
	if err != nil || string(got) != "ptfans:9432" || hash != "ptfans-hash" {
		t.Fatalf("got=%q hash=%q err=%v", got, hash, err)
	}
	got, hash, _, err = sources.Download(context.Background(), "rousipro:4411")
	if err != nil || string(got) != "rousipro:4411" || hash != "rousipro-hash" {
		t.Fatalf("got=%q hash=%q err=%v", got, hash, err)
	}
	if _, _, _, err = sources.Download(context.Background(), "rousi:9432"); !errors.Is(err, ErrUnknownPrivateTorrentSource) {
		t.Fatalf("unknown source err=%v", err)
	}
}

type sourceMarker struct{ name string }

type resourceList []torrentsearch.Resource

func (s resourceList) Search(context.Context, string) ([]torrentsearch.Resource, error) {
	return s, nil
}

func TestMultiResourceSearcherAddsMissingMagnetName(t *testing.T) {
	items := resourceList{
		{Kind: "bt", URI: "magnet:?xt=urn:btih:abc&tr=tracker"},
		{Kind: "bt", URI: "magnet:?xt=urn:btih:def&dn=original"},
		{Kind: "bt", URI: "magnet:?xt=urn:btih:ghi&dn="},
		{Kind: "pt", URI: "pt:123"},
		{Kind: "bt", URI: "magnet:?xt=urn:btih:jkl#fragment"},
	}
	got, err := (MultiResourceSearcher{Sources: []ResourceSearcher{items}}).Search(context.Background(), "番号 & 001")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"magnet:?xt=urn:btih:abc&tr=tracker&dn=%E7%95%AA%E5%8F%B7+%26+001", "magnet:?xt=urn:btih:def&dn=original", "magnet:?xt=urn:btih:ghi&dn=", "pt:123", "magnet:?xt=urn:btih:jkl&dn=%E7%95%AA%E5%8F%B7+%26+001#fragment"}
	for i := range want {
		if got[i].URI != want[i] {
			t.Errorf("item %d: got %q, want %q", i, got[i].URI, want[i])
		}
	}
}

func (s sourceMarker) Search(context.Context, string) ([]torrentsearch.Resource, error) {
	return []torrentsearch.Resource{{Site: s.name}}, nil
}

// TestSubscriptionDownloadRuntimeUsesLatestSettings prevents startup-only credentials from shadowing saved settings.
func TestSubscriptionDownloadRuntimeUsesLatestSettings(t *testing.T) {
	value := "old"
	service := NewSubscriptionDownloadService(nil, nil, nil, func(context.Context) (map[string]string, error) {
		return map[string]string{"SITE": value}, nil
	})
	service.SetRuntimeFactory(func(settings map[string]string) (ResourceSearcher, PrivateTorrentSource, map[string]MagnetDownloader) {
		return sourceMarker{name: settings["SITE"]}, nil, nil
	})
	_, firstSource, _, _, err := service.runtime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, err := firstSource.Search(context.Background(), "TEST-001")
	if err != nil || len(first) != 1 || first[0].Site != "old" {
		t.Fatalf("first=%v err=%v", first, err)
	}
	value = "new"
	_, secondSource, _, _, err := service.runtime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondSource.Search(context.Background(), "TEST-001")
	if err != nil || len(second) != 1 || second[0].Site != "new" {
		t.Fatalf("second=%v err=%v", second, err)
	}
}
