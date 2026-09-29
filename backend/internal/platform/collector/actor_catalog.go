package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"bytemuse/backend/internal/ports"
	"github.com/PuerkitoBio/goquery"
)

const gfriendsIndexURL = "https://raw.githubusercontent.com/gfriends/gfriends/master/Filetree.json"

var photoVariant = regexp.MustCompile(`-[0-9]+$`)

// GfriendsActors 获取官方索引，按原始目录顺序应用头像质量优先级；不下载图片二进制。
func GfriendsActors(ctx context.Context, f Fetcher) ([]ports.ActorProfile, error) {
	raw, err := f.Get(ctx, gfriendsIndexURL)
	if err != nil {
		return nil, err
	}
	return parseGfriends(raw)
}

// parseGfriends 保留 JSON 对象顺序（官方质量升序），按目标文件名合并别名和多张头像。
func parseGfriends(raw []byte) ([]ports.ActorProfile, error) {
	var envelope struct{ Content json.RawMessage }
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, ErrParse
	}
	dec := json.NewDecoder(bytes.NewReader(envelope.Content))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrParse
	}
	profiles := map[string]*ports.ActorProfile{}
	aliases := map[string]map[string]bool{}
	for dec.More() {
		token, err = dec.Token()
		if err != nil {
			return nil, ErrParse
		}
		company, ok := token.(string)
		if !ok || strings.ContainsAny(company, "/\\") || company == ".." {
			return nil, ErrParse
		}
		var files map[string]string
		if dec.Decode(&files) != nil {
			return nil, ErrParse
		}
		keys := make([]string, 0, len(files))
		for key := range files {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, aliasFile := range keys {
			target, err := url.Parse(files[aliasFile])
			if err != nil || target.IsAbs() || target.Host != "" || path.Base(target.Path) != target.Path {
				return nil, ErrParse
			}
			switch strings.ToLower(path.Ext(target.Path)) {
			case ".jpg", ".jpeg", ".png", ".webp":
			default:
				return nil, ErrParse
			}
			base := strings.TrimSuffix(target.Path, path.Ext(target.Path))
			name := strings.TrimSpace(photoVariant.ReplaceAllString(strings.TrimPrefix(base, "AI-Fix-"), ""))
			alias := strings.TrimSpace(photoVariant.ReplaceAllString(strings.TrimSuffix(aliasFile, path.Ext(aliasFile)), ""))
			if strings.TrimSpace(name) == "" {
				return nil, ErrParse
			}
			if profiles[name] == nil {
				profiles[name] = &ports.ActorProfile{Name: name}
				aliases[name] = map[string]bool{}
			}
			// 后面的目录优先；同目录同演员按稳定的键顺序取最后一个映射。
			photo := "https://raw.githubusercontent.com/gfriends/gfriends/master/Content/" + url.PathEscape(company) + "/" + url.PathEscape(target.Path)
			if target.RawQuery != "" {
				photo += "?" + target.RawQuery
			}
			profiles[name].Photo = photo
			aliases[name][alias] = true
		}
	}
	if _, err = dec.Token(); err != nil || len(profiles) == 0 {
		return nil, ErrParse
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ports.ActorProfile, 0, len(names))
	for _, name := range names {
		item := profiles[name]
		for alias := range aliases[name] {
			if alias != name && alias != "" {
				item.Aliases = append(item.Aliases, alias)
			}
		}
		sort.Strings(item.Aliases)
		result = append(result, *item)
	}
	return result, nil
}

// HotActors 只读取 JavDB 有码演员月榜区块，不把新人或完整演员目录误算为热门。
func HotActors(ctx context.Context, f Fetcher) ([]ports.ActorProfile, error) {
	raw, err := f.Get(ctx, "https://javdb.com/actors")
	if err != nil {
		return nil, err
	}
	return parseHotActors(raw)
}

func parseHotActors(raw []byte) ([]ports.ActorProfile, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrParse
	}
	var items []ports.ActorProfile
	seen := map[string]bool{}
	doc.Find("h3").Each(func(_ int, h *goquery.Selection) {
		if strings.TrimSpace(h.Text()) != "月榜" {
			return
		}
		h.Next().Find(".actor-box a").Each(func(_ int, a *goquery.Selection) {
			name := strings.TrimSpace(a.Find("strong").Text())
			href, _ := a.Attr("href")
			if name == "" || !strings.HasPrefix(href, "/actors/") || seen[name] {
				return
			}
			photo, _ := a.Find("img.avatar").Attr("src")
			title, _ := a.Attr("title")
			item := ports.ActorProfile{Name: name, Photo: photo}
			for _, alias := range strings.Split(title, ",") {
				if alias = strings.TrimSpace(alias); alias != "" && alias != name {
					item.Aliases = append(item.Aliases, alias)
				}
			}
			items = append(items, item)
			seen[name] = true
		})
	})
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: actor monthly ranking missing", ErrParse)
	}
	return items, nil
}
