package bootstrap

import (
	"net/http"
	"strings"

	"bytemuse/backend/internal/application"
	"bytemuse/backend/internal/platform/torrentsearch"
)

// configuredPrivateSites 为兼容调用保留同一客户端入口；搜索和取种各自创建适配器。
// PTTime 仅使用 Cookie；其历史 PassKey/UID 配置保留但不再参与运行。
func configuredPrivateSites(values map[string]string, client *http.Client) ([]application.ResourceSearcher, map[string]application.PrivateTorrentSource) {
	return configuredPrivateSitesWithClients(values, client, client)
}

// configuredPrivateSitesWithClients 为搜索和取种分别注入客户端，避免搜索回退策略影响下载副作用请求。
func configuredPrivateSitesWithClients(values map[string]string, downloadClient, searchClient *http.Client) ([]application.ResourceSearcher, map[string]application.PrivateTorrentSource) {
	sources := make([]application.ResourceSearcher, 0, 5)
	private := map[string]application.PrivateTorrentSource{}
	type site interface {
		application.ResourceSearcher
		application.PrivateTorrentSource
	}
	add := func(prefix string, search, download site) {
		sources = append(sources, search)
		private[prefix] = download
	}
	if key := strings.TrimSpace(values["MTEAM_API_KEY"]); key != "" {
		add("mteam", torrentsearch.NewMTeamSearcher(searchClient, "", key), torrentsearch.NewMTeamSearcher(downloadClient, "", key))
	}
	if cookie := strings.TrimSpace(values["PTT_COOKIE"]); cookie != "" {
		add("pttime", torrentsearch.NewPTTimeSearcher(searchClient, "", cookie), torrentsearch.NewPTTimeSearcher(downloadClient, "", cookie))
	}
	for _, prefix := range []string{"PTFANS", "NICEPT", "ROUSIPRO"} {
		if values[prefix+"_AUTH_TYPE"] == "cookie" {
			cookie := strings.TrimSpace(values[prefix+"_COOKIE"])
			if cookie == "" {
				continue
			}
			switch prefix {
			case "PTFANS":
				add("ptfans", torrentsearch.NewPTFansSearcher(searchClient, "", cookie), torrentsearch.NewPTFansSearcher(downloadClient, "", cookie))
			case "NICEPT":
				add("nicept", torrentsearch.NewNicePTSearcher(searchClient, "", cookie), torrentsearch.NewNicePTSearcher(downloadClient, "", cookie))
			case "ROUSIPRO":
				add("rousipro", torrentsearch.NewRousiProSearcher(searchClient, "", cookie), torrentsearch.NewRousiProSearcher(downloadClient, "", cookie))
			}
		} else {
			key := strings.TrimSpace(values[prefix+"_API_KEY"])
			if key == "" {
				continue
			}
			switch prefix {
			case "PTFANS":
				add("ptfans", torrentsearch.NewPTFansKeySearcher(searchClient, "", key), torrentsearch.NewPTFansKeySearcher(downloadClient, "", key))
			case "NICEPT":
				add("nicept", torrentsearch.NewNicePTKeySearcher(searchClient, "", key), torrentsearch.NewNicePTKeySearcher(downloadClient, "", key))
			case "ROUSIPRO":
				add("rousipro", torrentsearch.NewRousiProKeySearcher(searchClient, "", key), torrentsearch.NewRousiProKeySearcher(downloadClient, "", key))
			}
		}
	}
	return sources, private
}
