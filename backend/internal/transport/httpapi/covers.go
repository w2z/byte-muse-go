package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"bytemuse/backend/internal/platform/covercache"
)

// coverCacheMaxAge 是浏览器复用本地封面的秒数。本地文件按番号固定，一天足够页面刷新复用。
const coverCacheMaxAge = "86400"

// serveCover 返回番号对应的本地封面：命中缓存直接读取，未命中先下载再落盘。
// 源地址必须是可落盘的 http/https 绝对地址；目录不可写、源站失败或番号不能作为文件名时
// 回退到源地址，页面表现与没有缓存时一致，缓存故障不会让封面消失。
func serveCover(cache *covercache.Cache) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		source := strings.TrimSpace(request.URL.Query().Get("source"))
		if !covercache.AllowedSource(source) {
			writeError(response, http.StatusBadRequest, "invalid_cover_source", "封面源地址无效")
			return
		}
		if cache == nil {
			http.Redirect(response, request, source, http.StatusFound)
			return
		}
		file, err := cache.Ensure(request.Context(), chi.URLParam(request, "code"), source)
		if err != nil {
			http.Redirect(response, request, source, http.StatusFound)
			return
		}
		response.Header().Set("Cache-Control", "private, max-age="+coverCacheMaxAge)
		http.ServeFile(response, request, file.Path)
	}
}
