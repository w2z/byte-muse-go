package torrentsearch

import "testing"

func TestTorrentInfoHashHashesRawInfoDictionary(t *testing.T) {
	data := []byte("d8:announce14:http://tracker4:infod4:name4:testee")
	// The expected hash is a hand-checked SHA-1 of d4:name4:teste.
	got, e := TorrentInfoHash(data)
	if e != nil {
		t.Fatal(e)
	}
	if got != "1ade8a1a581f338e4fce4ce784da3f7d03f81f3a" {
		t.Fatalf("hash=%s", got)
	}
}

func TestTorrentInfoHashRejectsMalformedOrMissingInfo(t *testing.T) {
	for _, data := range []string{"d3:foo3:bare", "d4:infod4:name5:teste", "bad"} {
		if _, e := TorrentInfoHash([]byte(data)); e == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}
