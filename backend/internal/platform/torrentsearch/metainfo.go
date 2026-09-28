package torrentsearch

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
)

// TorrentInfoHash hashes the exact bencoded info dictionary, preserving private tracker identity.
func TorrentInfoHash(data []byte) (string, error) {
	if len(data) == 0 || data[0] != 'd' {
		return "", fmt.Errorf("torrent root is not a dictionary")
	}
	pos := 1
	var start, end int
	for pos < len(data) && data[pos] != 'e' {
		key, next, e := bString(data, pos)
		if e != nil {
			return "", e
		}
		pos = next
		valueStart := pos
		pos, e = bValue(data, pos, 0)
		if e != nil {
			return "", e
		}
		if string(key) == "info" {
			if start != 0 {
				return "", fmt.Errorf("duplicate info dictionary")
			}
			if data[valueStart] != 'd' {
				return "", fmt.Errorf("info is not a dictionary")
			}
			start, end = valueStart, pos
		}
	}
	if pos != len(data)-1 || data[pos] != 'e' || start == 0 {
		return "", fmt.Errorf("invalid torrent metainfo")
	}
	sum := sha1.Sum(data[start:end])
	return hex.EncodeToString(sum[:]), nil
}

func bString(data []byte, pos int) ([]byte, int, error) {
	start := pos
	for pos < len(data) && data[pos] >= '0' && data[pos] <= '9' {
		pos++
	}
	if start == pos || pos >= len(data) || data[pos] != ':' {
		return nil, 0, fmt.Errorf("invalid bencoded string")
	}
	count, e := strconv.Atoi(string(data[start:pos]))
	if e != nil || count < 0 || count > len(data)-pos-1 {
		return nil, 0, fmt.Errorf("invalid bencoded string length")
	}
	pos++
	return data[pos : pos+count], pos + count, nil
}

func bValue(data []byte, pos, depth int) (int, error) {
	if pos >= len(data) || depth > 32 {
		return 0, fmt.Errorf("invalid bencoded value")
	}
	switch data[pos] {
	case 'i':
		pos++
		start := pos
		for pos < len(data) && data[pos] != 'e' {
			pos++
		}
		if pos >= len(data) || start == pos {
			return 0, fmt.Errorf("invalid bencoded integer")
		}
		if _, e := strconv.ParseInt(string(data[start:pos]), 10, 64); e != nil {
			return 0, e
		}
		return pos + 1, nil
	case 'l', 'd':
		dict := data[pos] == 'd'
		pos++
		for pos < len(data) && data[pos] != 'e' {
			if dict {
				_, next, e := bString(data, pos)
				if e != nil {
					return 0, e
				}
				pos = next
			}
			next, e := bValue(data, pos, depth+1)
			if e != nil {
				return 0, e
			}
			pos = next
		}
		if pos >= len(data) {
			return 0, fmt.Errorf("unterminated bencoded container")
		}
		return pos + 1, nil
	default:
		_, next, e := bString(data, pos)
		return next, e
	}
}
