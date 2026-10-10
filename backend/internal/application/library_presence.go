package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

// LibraryPresenceService 按记录来源检查；历史无来源记录只在已配置的数据源中查找。
// 连接、权限及分页错误均为未知，不将旧的 library_status 当成实时存在证据。
type LibraryPresenceService struct {
	pan             pan115FileAPI
	cd              strmCloudDriveAPI
	settings        map[string]string
	http            *http.Client
	inventories     map[string]map[string]ports.LibrarySource
	inventoryErrors map[string]error
}

// NewLibraryPresenceService 为一次处理批次建立核验器，复用现有网盘客户端的认证和限流。
func NewLibraryPresenceService(pan pan115FileAPI, cd strmCloudDriveAPI, settings map[string]string, client *http.Client) *LibraryPresenceService {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &LibraryPresenceService{pan: pan, cd: cd, settings: settings, http: client, inventories: map[string]map[string]ports.LibrarySource{}, inventoryErrors: map[string]error{}}
}

// Check 任意来源存在即满足；全部明确缺失才允许搜索，未知来源保留待核验。
func (s *LibraryPresenceService) Check(ctx context.Context, code string, sources []ports.LibrarySource) (*ports.LibrarySource, error) {
	if len(sources) == 0 {
		return s.discover(ctx, code)
	}
	var last error
	for _, source := range sources {
		found, err := s.checkSource(ctx, code, source)
		if found {
			return &source, nil
		}
		if err != nil {
			last = err
		}
	}
	return nil, last
}

func (s *LibraryPresenceService) checkSource(ctx context.Context, code string, source ports.LibrarySource) (bool, error) {
	switch source.Kind {
	case "local":
		info, err := os.Stat(source.Location)
		if errors.Is(err, os.ErrNotExist) {
			// 目录也不可达时可能是挂载离线，不能认定影片丢失。
			if _, parentErr := os.Stat(filepath.Dir(source.Location)); parentErr != nil {
				return false, fmt.Errorf("本地来源目录不可用")
			}
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("本地来源检查失败")
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return false, nil
		}
		if strings.EqualFold(filepath.Ext(source.Location), ".strm") {
			return false, fmt.Errorf("STRM 仅是指针，需要核实网盘或媒体服务器来源")
		}
		return true, nil
	case "115":
		if s.pan == nil {
			return false, ErrPan115NotLinked
		}
		if source.Scope != "" {
			identity, ok := s.pan.(interface {
				EventAccountID(context.Context) (string, error)
			})
			if !ok {
				return false, fmt.Errorf("115 账号身份不可验证")
			}
			account, err := identity.EventAccountID(ctx)
			if err != nil {
				return false, err
			}
			if account != source.Scope {
				return false, fmt.Errorf("115 来源账号已变化")
			}
		}
		// 文件移动后父目录会过期，优先用稳定 ID 查询；未知错误不能降级成不存在。
		if api, ok := s.pan.(interface {
			EventFileInfo(context.Context, string) (pan115.FileInfo, error)
		}); ok {
			info, err := api.EventFileInfo(ctx, source.ItemID)
			if err == nil {
				return !info.IsDirectory && domain.ExtractCode(info.Name) == code, nil
			}
			if !errors.Is(err, pan115.ErrFileNotFound) {
				return false, fmt.Errorf("115 来源文件检查失败")
			}
		}
		for offset := 0; ; {
			page, err := s.pan.Files(ctx, source.Location, offset, strmListLimit)
			if err != nil {
				return false, fmt.Errorf("115 来源目录检查失败")
			}
			for _, file := range page.Files {
				if file.ID == source.ItemID && !file.IsDirectory && domain.ExtractCode(file.Name) == code {
					return true, nil
				}
			}
			if !page.HasMore {
				return false, nil
			}
			if len(page.Files) == 0 {
				return false, fmt.Errorf("115 来源分页不完整")
			}
			offset += len(page.Files)
		}
	case "cd2":
		if source.Scope == "" || source.Scope != s.cloudScope() {
			return false, fmt.Errorf("CloudDrive2 来源身份不匹配")
		}
		if s.cd == nil || !s.cd.Configured(ctx) {
			return false, fmt.Errorf("CloudDrive2 来源不可用")
		}
		entries, err := s.cd.ListSubFiles(ctx, source.Location)
		if err != nil {
			return false, fmt.Errorf("CloudDrive2 来源检查失败")
		}
		for _, file := range entries {
			if !file.Directory && file.FullPath == source.ItemID && domain.ExtractCode(file.Name) == code {
				return true, nil
			}
		}
		return false, nil
	case "emby", "jellyfin", "plex":
		base := strings.TrimRight(s.settings[strings.ToUpper(source.Kind)+"_URL"], "/")
		if base == "" || base != source.Location {
			return false, fmt.Errorf("媒体服务器来源配置已变化")
		}
		found, err := s.server(ctx, source.Kind, code, source.ItemID)
		return found != nil, err
	default:
		return false, fmt.Errorf("无法识别媒体来源")
	}
}

func (s *LibraryPresenceService) discover(ctx context.Context, code string) (*ports.LibrarySource, error) {
	var last error
	checked := false
	for _, kind := range []string{"emby", "plex", "jellyfin"} {
		if s.settings[strings.ToUpper(kind)+"_URL"] == "" {
			continue
		}
		checked = true
		source, err := s.server(ctx, kind, code, "")
		if source != nil {
			return source, nil
		}
		if err != nil {
			last = err
		}
	}
	directories, err := parsePan115ScanPaths(s.settings[pan115ScanPathsSettingKey])
	if err != nil {
		last = err
	}
	for _, directory := range directories {
		checked = true
		if s.pan == nil {
			last = ErrPan115NotLinked
			continue
		}
		key := "115:" + directory.ID
		if _, loaded := s.inventories[key]; !loaded {
			index := map[string]ports.LibrarySource{}
			s.inventories[key] = index
			account := ""
			if identity, ok := s.pan.(interface {
				EventAccountID(context.Context) (string, error)
			}); ok {
				var e error
				account, e = identity.EventAccountID(ctx)
				if e != nil {
					s.inventoryErrors[key] = e
					continue
				}
			}
			s.inventoryErrors[key] = walkPan115Files(ctx, s.pan, directory.ID, strmFileFilter{formats: defaultStrmFormats}, func(file strmSourceFile) error {
				if code := domain.ExtractCode(file.Name); code != "" {
					index[code] = ports.LibrarySource{Kind: "115", Scope: account, Location: file.ParentID, ItemID: file.ID}
				}
				return nil
			})
		}
		if source, ok := s.inventories[key][code]; ok {
			return &source, nil
		}
		if s.inventoryErrors[key] != nil {
			last = fmt.Errorf("115 历史来源检查失败")
		}
	}
	mappings, mappingErr := parseStrmMappings(s.settings[strmPathsSettingKey])
	if mappingErr != nil {
		last = mappingErr
	}
	for _, mapping := range mappings {
		if mapping.Kind != domain.StrmKindCloudDrive2 {
			continue
		}
		checked = true
		if s.cd == nil || !s.cd.Configured(ctx) {
			last = fmt.Errorf("CloudDrive2 来源不可用")
			continue
		}
		key := "cd2:" + mapping.ID
		if _, loaded := s.inventories[key]; !loaded {
			index := map[string]ports.LibrarySource{}
			s.inventories[key] = index
			walker := &StrmService{cloud: s.cd}
			s.inventoryErrors[key] = walker.walkCloudDrive(ctx, mapping.ID, strmFileFilter{formats: defaultStrmFormats}, func(file strmSourceFile) error {
				if code := domain.ExtractCode(file.Name); code != "" {
					index[code] = ports.LibrarySource{Kind: "cd2", Scope: s.cloudScope(), Location: filepath.ToSlash(filepath.Dir(file.ID)), ItemID: file.ID}
				}
				return nil
			})
		}
		if source, ok := s.inventories[key][code]; ok {
			return &source, nil
		}
		if s.inventoryErrors[key] != nil {
			last = fmt.Errorf("CloudDrive2 历史来源检查失败")
		}
	}

	// 只读已有下载目录，不遍历任意主机目录；STRM 指针不等同于完整影片。
	for _, key := range []string{"QBITTORRENT_DOWNLOAD_PATH", "ARIA2_DOWNLOAD_PATH", "TRANSMISSION_DOWNLOAD_PATH"} {
		root := strings.TrimSpace(s.settings[key])
		if root == "" {
			continue
		}
		checked = true
		cacheKey := "local:" + root
		if _, loaded := s.inventories[cacheKey]; !loaded {
			index := map[string]ports.LibrarySource{}
			s.inventories[cacheKey] = index
			s.inventoryErrors[cacheKey] = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
					return nil
				}
				if domain.ExtractCode(d.Name()) == "" || !isStrmMedia(d.Name(), defaultStrmFormats) || strings.EqualFold(filepath.Ext(path), ".strm") {
					return nil
				}
				info, e := d.Info()
				if e != nil {
					return e
				}
				if info.Size() > 0 {
					index[domain.ExtractCode(d.Name())] = ports.LibrarySource{Kind: "local", Location: path}
				}
				return nil
			})
		}
		if found, ok := s.inventories[cacheKey][code]; ok {
			return &found, nil
		}
		if s.inventoryErrors[cacheKey] != nil {
			last = fmt.Errorf("本地下载目录检查失败")
		}
	}
	if !checked {
		return nil, fmt.Errorf("历史影片未记录来源，且没有可核验的数据源")
	}
	// 未记录原来源时，当前配置可能已经换过；未命中不能证明旧文件已经消失。
	if last != nil {
		return nil, last
	}
	return nil, fmt.Errorf("历史影片未找到来源，需要重新扫描入库后核实")
}

type presenceServerItem struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	Path     string `json:"Path"`
	IsFolder bool   `json:"IsFolder"`
}

// server 按番号查询并严格比较媒体路径/名称，分页完整才能得出不存在；不触发播放或刷新。
func (s *LibraryPresenceService) server(ctx context.Context, kind, code, itemID string) (*ports.LibrarySource, error) {
	base := strings.TrimRight(s.settings[strings.ToUpper(kind)+"_URL"], "/")
	key := s.settings[strings.ToUpper(kind)+"_API_KEY"]
	if kind == "plex" {
		key = s.settings["PLEX_TOKEN"]
	}
	if base == "" || key == "" {
		return nil, fmt.Errorf("媒体服务器配置不完整")
	}
	parsed, parseErr := url.Parse(base)
	if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" {
		return nil, fmt.Errorf("媒体服务器地址无效或含凭据")
	}
	seen := map[string]bool{}
	for start := 0; ; {
		endpoint := base + "/Items"
		if kind == "emby" {
			endpoint = base + "/emby/Items"
		}
		if kind == "plex" {
			endpoint = base + "/library/all"
		}
		params := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {"Movie,Episode"}, "Fields": {"Path"}, "SearchTerm": {code}, "Limit": {"200"}, "StartIndex": {strconv.Itoa(start)}}
		if kind == "plex" {
			params = url.Values{"type": {"1"}, "title": {code}, "X-Plex-Container-Start": {strconv.Itoa(start)}, "X-Plex-Container-Size": {"200"}}
		}
		if itemID != "" {
			if kind == "plex" {
				endpoint = base + "/library/metadata/" + url.PathEscape(itemID)
				params = url.Values{}
			} else {
				params.Del("SearchTerm")
				params.Set("Ids", itemID)
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("媒体服务器地址无效")
		}
		if kind == "plex" {
			req.Header.Set("X-Plex-Token", key)
			req.Header.Set("Accept", "application/xml")
		} else {
			req.Header.Set("X-Emby-Token", key)
		}
		res, err := s.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("媒体服务器连接失败")
		}
		if itemID != "" && res.StatusCode == http.StatusNotFound {
			res.Body.Close()
			return nil, nil
		}
		if res.StatusCode/100 != 2 {
			res.Body.Close()
			return nil, fmt.Errorf("媒体服务器检查返回 %d", res.StatusCode)
		}
		var items []presenceServerItem
		total := 0
		if kind == "plex" {
			var page struct {
				XMLName xml.Name `xml:"MediaContainer"`
				Total   int      `xml:"totalSize,attr"`
				Videos  []struct {
					ID    string `xml:"ratingKey,attr"`
					Title string `xml:"title,attr"`
					Media []struct {
						Parts []struct {
							File string `xml:"file,attr"`
						} `xml:"Part"`
					} `xml:"Media"`
				} `xml:"Video"`
			}
			err = xml.NewDecoder(res.Body).Decode(&page)
			total = page.Total
			for _, v := range page.Videos {
				item := presenceServerItem{ID: v.ID, Name: v.Title}
				for _, m := range v.Media {
					for _, p := range m.Parts {
						if item.Path == "" || domain.ExtractCode(filepath.Base(strings.ReplaceAll(p.File, "\\", "/"))) == code {
							item.Path = p.File
						}
					}
				}
				items = append(items, item)
			}
		} else {
			var page struct {
				Items            []presenceServerItem
				TotalRecordCount int
			}
			err = json.NewDecoder(res.Body).Decode(&page)
			if err == nil && page.Items == nil {
				err = fmt.Errorf("missing Items")
			}
			items = page.Items
			total = page.TotalRecordCount
		}
		res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("媒体服务器响应无效")
		}
		for _, item := range items {
			if seen[item.ID] {
				return nil, fmt.Errorf("媒体服务器重复返回分页")
			}
			seen[item.ID] = true
			if item.ID == "" || item.IsFolder || item.Path == "" || (itemID != "" && item.ID != itemID) {
				continue
			}
			name := filepath.Base(strings.ReplaceAll(item.Path, "\\", "/"))
			pathCode := domain.ExtractCode(name)
			if pathCode == code || pathCode == "" && domain.ExtractCode(item.Name) == code {
				return &ports.LibrarySource{Kind: kind, Location: base, ItemID: item.ID}, nil
			}
		}
		if len(items) == 0 && total > start {
			return nil, fmt.Errorf("媒体服务器分页不完整")
		}
		if itemID != "" || total > 0 && start+len(items) >= total || total == 0 && len(items) < 200 {
			return nil, nil
		}
		start += len(items)
	}
}

// cloudScope 将服务器及账号身份摘要化，不保存认证信息。
func (s *LibraryPresenceService) cloudScope() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s.settings["CLOUDNAS_URL"]+"\n"+s.settings["CLOUDNAS_USERNAME"])))
}
