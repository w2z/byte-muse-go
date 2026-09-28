package bootstrap

import (
	"net/http"
	"strings"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/torrentsearch"
)

// configuredPrivateSites 每次处理批次依据已选鉴权方式创建搜索和取种的同一实例。
// PTTime 仅使用 Cookie；其历史 PassKey/UID 配置保留但不再参与运行。
func configuredPrivateSites(values map[string]string, client *http.Client) ([]application.ResourceSearcher, map[string]application.PrivateTorrentSource) {
	sources := make([]application.ResourceSearcher, 0, 5)
	private := map[string]application.PrivateTorrentSource{}
	type site interface {
		application.ResourceSearcher
		application.PrivateTorrentSource
	}
	add := func(prefix string, adapter site) { sources = append(sources, adapter); private[prefix] = adapter }
	if key := strings.TrimSpace(values["MTEAM_API_KEY"]); key != "" {
		add("mteam", torrentsearch.NewMTeamSearcher(client, "", key))
	}
	if cookie := strings.TrimSpace(values["PTT_COOKIE"]); cookie != "" {
		add("pttime", torrentsearch.NewPTTimeSearcher(client, "", cookie))
	}
	for _, prefix := range []string{"PTFANS", "NICEPT", "ROUSIPRO"} {
		if values[prefix+"_AUTH_TYPE"] == "cookie" {
			cookie := strings.TrimSpace(values[prefix+"_COOKIE"])
			if cookie == "" {
				continue
			}
			switch prefix {
			case "PTFANS":
				add("ptfans", torrentsearch.NewPTFansSearcher(client, "", cookie))
			case "NICEPT":
				add("nicept", torrentsearch.NewNicePTSearcher(client, "", cookie))
			case "ROUSIPRO":
				add("rousipro", torrentsearch.NewRousiProSearcher(client, "", cookie))
			}
		} else {
			key := strings.TrimSpace(values[prefix+"_API_KEY"])
			if key == "" {
				continue
			}
			switch prefix {
			case "PTFANS":
				add("ptfans", torrentsearch.NewPTFansKeySearcher(client, "", key))
			case "NICEPT":
				add("nicept", torrentsearch.NewNicePTKeySearcher(client, "", key))
			case "ROUSIPRO":
				add("rousipro", torrentsearch.NewRousiProKeySearcher(client, "", key))
			}
		}
	}
	return sources, private
}
