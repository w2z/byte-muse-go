package torrentsearch

import (
	"net/http"
	"regexp"
)

const ptfansOrigin = "https://ptfans.cc"

var ptfansID = regexp.MustCompile("^[0-9]+$")

// PTFansSearcher 复用 NexusPHP 网页分页与安全取种实现。
type PTFansSearcher = NexusCookieSearcher

// NewPTFansSearcher 搜索 PTFans 的 9KG 专区，保持原 Cookie 构造接口。
func NewPTFansSearcher(client *http.Client, origin, cookie string) *PTFansSearcher {
	if origin == "" {
		origin = ptfansOrigin
	}
	return newNexusCookieSearcher(client, origin, cookie, "PTFans", "ptfans", "/special.php", 4)
}
