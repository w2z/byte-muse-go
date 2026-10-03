package pan115

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pngSignature 是 PNG 文件头，用于让 http.DetectContentType 识别二维码图片。
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// newTestClient 返回指向测试服务器的客户端，并关闭节流与退避等待以免拖慢测试；
// 需要验证退避行为的用例自行改写 gap/playGap/cooldown/backoff。
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := New(server.Client())
	client.gap = 0
	client.playGap = 0
	client.cooldown = 0
	client.backoff = 0
	client.passport = server.URL
	client.qrcode = server.URL
	client.api = server.URL
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("写入响应失败: %v", err)
	}
}

// TestLoginStatusNormalizesProviderStates 验证只有 115 明确定义的状态才改变登录结论，
// 未识别或缺失的状态必须停留在「等待中」，不能中断用户仍在进行的扫码。
func TestLoginStatusNormalizesProviderStates(t *testing.T) {
	cases := []struct {
		body string
		want LoginState
	}{
		{`{"state":1,"code":0,"data":{"status":0}}`, LoginWaiting},
		{`{"state":1,"code":0,"data":{"status":1}}`, LoginScanned},
		{`{"state":1,"code":0,"data":{"status":2}}`, LoginAuthorized},
		{`{"state":1,"code":0,"data":{"status":-1}}`, LoginExpired},
		{`{"state":1,"code":0,"data":{"status":-2}}`, LoginCanceled},
		{`{"state":1,"code":0,"data":{"status":99}}`, LoginWaiting},
		{`{"state":1,"code":0,"data":{}}`, LoginWaiting},
	}
	for _, testCase := range cases {
		body := testCase.body
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, body) })
		got, err := client.LoginStatus(context.Background(), &Login{uid: "uid", issuedAt: 1, sign: "sign"})
		if err != nil {
			t.Fatalf("响应 %s 返回错误: %v", body, err)
		}
		if got != testCase.want {
			t.Fatalf("响应 %s 映射为 %q，期望 %q", body, got, testCase.want)
		}
	}
}

// TestLoginStatusSendsSignatureAsQuery 验证状态查询把 uid/time/sign 放在查询串里。
// 115 按查询串校验签名，放进请求体会返回 40199002 key invalid，使扫码永远无法被确认。
func TestLoginStatusSendsSignatureAsQuery(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/get/status/" {
			t.Errorf("请求 = %s %s，期望 GET /get/status/", r.Method, r.URL.Path)
		}
		if r.ContentLength > 0 {
			t.Errorf("状态查询不应携带请求体，实际 Content-Length = %d", r.ContentLength)
		}
		query := r.URL.Query()
		if query.Get("uid") != "uid-1" || query.Get("time") != "1700000000" || query.Get("sign") != "sign-1" {
			t.Errorf("查询串 = %q，期望携带 uid/time/sign", r.URL.RawQuery)
		}
		writeJSON(t, w, `{"state":1,"code":0,"data":{"status":1}}`)
	})
	state, err := client.LoginStatus(context.Background(), &Login{uid: "uid-1", issuedAt: 1700000000, sign: "sign-1"})
	if err != nil {
		t.Fatalf("查询扫码状态失败: %v", err)
	}
	if state != LoginScanned {
		t.Fatalf("扫码状态 = %q，期望 %q", state, LoginScanned)
	}
}

// TestBeginLoginRequiresPNGQRCode 验证设备码字段校验与二维码内容类型校验。
func TestBeginLoginRequiresPNGQRCode(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/open/authDeviceCode"):
			if err := r.ParseForm(); err != nil {
				t.Errorf("解析表单失败: %v", err)
			}
			if r.PostForm.Get("client_id") != openListAppID || r.PostForm.Get("code_challenge_method") != "sha256" {
				t.Errorf("设备码请求缺少公开应用标识或校验方法: %v", r.PostForm)
			}
			writeJSON(t, w, `{"state":1,"code":0,"data":{"uid":"uid-1","time":1700000000,"sign":"sign-1"}}`)
		case strings.HasSuffix(r.URL.Path, "/api/1.0/web/1.0/qrcode"):
			if r.URL.Query().Get("uid") != "uid-1" {
				t.Errorf("二维码请求未携带 uid: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngSignature)
		default:
			http.NotFound(w, r)
		}
	})
	login, err := client.BeginLogin(context.Background())
	if err != nil {
		t.Fatalf("开始登录失败: %v", err)
	}
	if string(login.QRCode) != string(pngSignature) {
		t.Fatalf("二维码内容 = %v，期望 PNG 头", login.QRCode)
	}
}

// TestBeginLoginRejectsMissingDeviceCode 验证缺少设备码字段时直接失败，不返回不可用的登录会话。
func TestBeginLoginRejectsMissingDeviceCode(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":1,"code":0,"data":{"uid":"","time":0,"sign":""}}`)
	})
	if _, err := client.BeginLogin(context.Background()); err == nil {
		t.Fatal("缺少设备码字段时应当报错")
	}
}

// TestBeginLoginRejectsNonPNGQRCode 验证非图片响应不会被当作二维码返回。
func TestBeginLoginRejectsNonPNGQRCode(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/open/authDeviceCode") {
			writeJSON(t, w, `{"state":1,"code":0,"data":{"uid":"uid-1","time":1700000000,"sign":"sign-1"}}`)
			return
		}
		writeJSON(t, w, `{"state":0,"code":1,"message":"boom"}`)
	})
	if _, err := client.BeginLogin(context.Background()); err == nil {
		t.Fatal("非 PNG 响应时应当报错")
	}
}

// TestRequestTokensRejectsIncompleteResponse 验证令牌响应缺少凭据或有效期时不落库。
func TestRequestTokensRejectsIncompleteResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":1,"code":0,"data":{"access_token":"a","refresh_token":"","expires_in":0}}`)
	})
	if _, err := client.RefreshToken(context.Background(), "refresh"); err == nil {
		t.Fatal("令牌响应不完整时应当报错")
	}
}

// TestAccountParsesSpace 验证账号与容量字段的解析。
func TestAccountParsesSpace(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Errorf("Authorization = %q，期望 Bearer token-1", got)
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":{"user_id":12345,"user_name":"张三","user_face_m":"https://img/face.png","vip_info":{"level_name":"VIP"},"rt_space_info":{"all_total":{"size":1099511627776,"size_format":"1.0TB"},"all_use":{"size":"1024","size_format":"1.0KB"},"all_remain":{"size":1099511626752,"size_format":"1.0TB"}}}}`)
	})
	account, err := client.Account(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	if account.ID != "12345" || account.Name != "张三" || account.Level != "VIP" {
		t.Fatalf("账号解析结果 = %+v", account)
	}
	if account.Space.Total.Size != 1099511627776 || account.Space.Used.Size != 1024 || account.Space.Used.Formatted != "1.0KB" {
		t.Fatalf("容量解析结果 = %+v", account.Space)
	}
}

// TestListRejectsMismatchedDirectory 验证 115 返回的目录与请求不一致时报错，
// 避免把别的目录内容当作已挂载目录。
func TestListRejectsMismatchedDirectory(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":true,"code":0,"data":[],"count":1,"path":[{"cid":0,"name":"文件"},{"cid":"999","name":"其他目录"}]}`)
	})
	if _, err := client.List(context.Background(), "token", "123", 0, 100); err == nil {
		t.Fatal("目录不一致时应当报错")
	}
}

// TestListParsesEntries 验证目录条目与分页字段解析。
func TestListParsesEntries(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("cid") != "123" || query.Get("offset") != "0" || query.Get("limit") != "2" || query.Get("show_dir") != "1" {
			t.Errorf("文件列表请求参数不符合契约: %s", r.URL.RawQuery)
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":[{"fid":"f1","pid":"123","fn":"子目录","fc":"0","fs":"0","pc":""},{"fid":"f2","pid":"123","fn":"影片.mp4","fc":"1","fs":"2048","pc":"pc-2"}],"count":3,"path":[{"cid":0,"name":"文件"},{"cid":"123","name":"媒体库"}]}`)
	})
	page, err := client.List(context.Background(), "token", "123", 0, 2)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if page.Total != 3 || !page.HasMore || len(page.Path) != 2 || page.Path[1].Name != "媒体库" {
		t.Fatalf("目录分页结果 = %+v", page)
	}
	if !page.Files[0].IsDirectory || page.Files[0].Name != "子目录" {
		t.Fatalf("目录条目解析错误: %+v", page.Files[0])
	}
	if page.Files[1].IsDirectory || page.Files[1].Size != 2048 || page.Files[1].PickCode != "pc-2" {
		t.Fatalf("文件条目解析错误: %+v", page.Files[1])
	}
}

// TestListParsesRootResponse 验证根目录（cid=0）的条目、总数与父目录树被正常解析。
// 115 对根目录同样回传 count 与 path（path 只有根目录自身），路径不该再走目录信息接口。
func TestListParsesRootResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cid") != "0" {
			t.Errorf("根目录请求 cid = %q，期望 0", r.URL.Query().Get("cid"))
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":[{"fid":"d1","pid":"0","fn":"云下载","fc":"0","fs":"0","pc":"pc-1"},{"fid":"d2","pid":"0","fn":"存档","fc":"0","fs":"0","pc":"pc-2"}],"count":2,"path":[{"name":"文件","cid":0,"pid":0}]}`)
	})
	page, err := client.List(context.Background(), "token", "0", 0, 100)
	if err != nil {
		t.Fatalf("读取根目录失败: %v", err)
	}
	if len(page.Files) != 2 || page.Files[0].Name != "云下载" || !page.Files[0].IsDirectory {
		t.Fatalf("根目录条目解析错误: %+v", page.Files)
	}
	if len(page.Path) != 1 || page.Path[0].ID != "0" || page.Path[0].Name != "文件" {
		t.Fatalf("根目录父目录树解析错误: %+v", page.Path)
	}
	if page.Total != 2 || page.HasMore {
		t.Fatalf("根目录分页 = total %d hasMore %v，期望 2/false", page.Total, page.HasMore)
	}
}

// TestListWithoutCountInfersHasMore 验证 115 未回传总数时按本页条目数推断下一页，
// 同时路径为空，交给上层兜底。
func TestListWithoutCountInfersHasMore(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":true,"code":0,"data":[{"fid":"d1","pid":"123","fn":"云下载","fc":"0","fs":"0","pc":""},{"fid":"d2","pid":"123","fn":"存档","fc":"0","fs":"0","pc":""}]}`)
	})
	page, err := client.List(context.Background(), "token", "123", 0, 2)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if page.Total != 2 || !page.HasMore {
		t.Fatalf("无总数分页 = total %d hasMore %v，期望 2/true", page.Total, page.HasMore)
	}
	if len(page.Path) != 0 {
		t.Fatalf("无父目录树时路径应为空，实际 %+v", page.Path)
	}
}

// TestListHandlesEmptyRootDirectory 验证空根目录返回空页而不是报错。
func TestListHandlesEmptyRootDirectory(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":true,"code":0,"data":[],"count":0,"path":[{"name":"文件","cid":0,"pid":0}]}`)
	})
	page, err := client.List(context.Background(), "token", "0", 0, 100)
	if err != nil {
		t.Fatalf("读取空根目录失败: %v", err)
	}
	if len(page.Files) != 0 || page.Total != 0 || page.HasMore {
		t.Fatalf("空根目录分页 = %+v", page)
	}
}

// TestAddOfflineUsesMultipartAndReportsDuplicate 验证离线提交使用 multipart 编码，
// 并把「已存在」识别为可安全忽略的结果。
func TestAddOfflineUsesMultipartAndReportsDuplicate(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("Content-Type = %q，期望 multipart/form-data", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("解析 multipart 失败: %v", err)
		}
		if r.Form.Get("urls") != "magnet:?xt=urn:btih:abc" || r.Form.Get("wp_path_id") != "123" {
			t.Errorf("离线提交表单 = %v", r.Form)
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":[{"state":true,"code":0,"info_hash":"ABC"}]}`)
	})
	hash, err := client.AddOffline(context.Background(), "token", "magnet:?xt=urn:btih:abc", "123")
	if err != nil {
		t.Fatalf("提交离线任务失败: %v", err)
	}
	if hash != "ABC" {
		t.Fatalf("离线任务哈希 = %q，期望 ABC", hash)
	}

	duplicate := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":true,"code":0,"data":[{"state":false,"code":10008,"message":"任务已存在"}]}`)
	})
	if _, err := duplicate.AddOffline(context.Background(), "token", "magnet:?xt=urn:btih:abc", "123"); !errors.Is(err, ErrOfflineExists) {
		t.Fatalf("重复提交错误 = %v，期望 ErrOfflineExists", err)
	}
}

// TestOfflineTasksNormalizesProgress 验证进度的小数、越界与数字字符串都能归一化。
func TestOfflineTasksNormalizesProgress(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("离线任务页码 = %q，期望 2", r.URL.Query().Get("page"))
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":{"page_count":3,"tasks":[{"info_hash":"h1","status":2,"percentDone":12.5,"file_id":"f1","wp_path_id":"123"},{"info_hash":"h2","status":1,"percentDone":"150","file_id":"","wp_path_id":"123"},{"info_hash":"h3","status":1,"percentDone":-3,"file_id":"","wp_path_id":"123"}]}}`)
	})
	page, err := client.OfflineTasks(context.Background(), "token", 2)
	if err != nil {
		t.Fatalf("读取离线任务失败: %v", err)
	}
	if page.PageCount != 3 || len(page.Tasks) != 3 {
		t.Fatalf("离线任务分页 = %+v", page)
	}
	if page.Tasks[0].Progress != 12 || page.Tasks[1].Progress != 100 || page.Tasks[2].Progress != 0 {
		t.Fatalf("离线进度归一化错误: %+v", page.Tasks)
	}
	if page.Tasks[0].FileID != "f1" || page.Tasks[0].Status != 2 {
		t.Fatalf("离线任务字段错误: %+v", page.Tasks[0])
	}
}

// TestOfflineQuotaParsesTotals 验证云下载配额按任务数解析 count/used/surplus，
// 并命中 115 开放平台的配额接口，而不是网页版离线配额接口。
func TestOfflineQuotaParsesTotals(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open/offline/get_quota_info" {
			t.Errorf("配额请求路径 = %q，期望 /open/offline/get_quota_info", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Errorf("Authorization = %q，期望 Bearer token-1", got)
		}
		writeJSON(t, w, `{"state":true,"message":"","code":0,"data":{"package":[{"surplus":1500,"used":0,"count":1500,"name":"VIP配额","expire_info":null}],"count":1500,"surplus":1500,"max_size":500,"used":0}}`)
	})
	quota, err := client.OfflineQuota(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("读取云下载配额失败: %v", err)
	}
	if quota.Total != 1500 || quota.Used != 0 || quota.Remaining != 1500 {
		t.Fatalf("云下载配额 = %+v，期望 total=1500 used=0 remaining=1500", quota)
	}
}

// TestOfflineQuotaTreatsMissingDataAsZero 验证 115 未返回 data 段时按零配额处理，不报错，
// 避免非会员账号让整个账号面板变成错误态。
func TestOfflineQuotaTreatsMissingDataAsZero(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":true,"message":"","code":0,"data":null}`)
	})
	quota, err := client.OfflineQuota(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("空 data 不应报错: %v", err)
	}
	if quota != (OfflineQuota{}) {
		t.Fatalf("空 data 的配额 = %+v，期望零值", quota)
	}
}

// TestUnauthorizedDetection 验证 HTTP 401 与业务错误码都能识别为令牌失效。
func TestUnauthorizedDetection(t *testing.T) {
	rejected := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := rejected.Account(context.Background(), "token")
	if !errors.Is(err, ErrUnauthorized) || !Unauthorized(err) {
		t.Fatalf("HTTP 401 错误 = %v，期望识别为令牌失效", err)
	}

	expired := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":false,"code":40140123,"message":"访问令牌已过期"}`)
	})
	_, err = expired.Account(context.Background(), "token")
	if !Unauthorized(err) {
		t.Fatalf("业务错误码错误 = %v，期望识别为令牌失效", err)
	}

	other := &APIError{Code: 10008, Message: "任务已存在"}
	if Unauthorized(other) {
		t.Fatal("无关错误码不应被识别为令牌失效")
	}
}

// TestAPIErrorCarriesProviderMessage 验证业务错误保留 115 的错误码与消息。
func TestAPIErrorCarriesProviderMessage(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":false,"code":430004,"message":"文件不存在"}`)
	})
	_, err := client.OfflineTasks(context.Background(), "token", 1)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误类型 = %v，期望 APIError", err)
	}
	if apiErr.Code != 430004 || apiErr.Message != "文件不存在" {
		t.Fatalf("业务错误 = %+v", apiErr)
	}
}

// TestNormalizeCookieClientType 验证扫码渠道归一化：合法值原样保留，空值与未知值回落默认渠道。
func TestNormalizeCookieClientType(t *testing.T) {
	for _, value := range cookieClientTypes {
		if got := NormalizeCookieClientType(value); got != value {
			t.Fatalf("渠道 %q 归一化 = %q", value, got)
		}
	}
	for _, value := range []string{"", "  ", "unknown", "WEB"} {
		if got := NormalizeCookieClientType(value); got != CookieClientAlipayMini {
			t.Fatalf("渠道 %q 归一化 = %q，期望 %q", value, got, CookieClientAlipayMini)
		}
	}
}

// TestBeginCookieLoginUsesWebTokenAndKeepsChannel 验证二维码始终取自 web 入口，
// 渠道只记录在会话上，供换取 Cookie 时选择客户端。
func TestBeginCookieLoginUsesWebTokenAndKeepsChannel(t *testing.T) {
	var tokenPath string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/1.0/web/1.0/token/"):
			tokenPath = r.URL.Path
			writeJSON(t, w, `{"state":1,"data":{"uid":"uid-1","time":1700000000,"sign":"sign-1"}}`)
		case strings.HasSuffix(r.URL.Path, "/api/1.0/web/1.0/qrcode"):
			w.Header().Set("Content-Type", "image/png")
			if _, err := w.Write(pngSignature); err != nil {
				t.Errorf("写入二维码失败: %v", err)
			}
		default:
			t.Errorf("未预期的请求路径 %s", r.URL.Path)
		}
	})
	login, err := client.BeginCookieLogin(context.Background(), CookieClientIPad)
	if err != nil {
		t.Fatalf("申请 Cookie 扫码失败: %v", err)
	}
	if tokenPath == "" {
		t.Fatal("未使用 web 入口申请扫码令牌")
	}
	if string(login.QRCode) != string(pngSignature) {
		t.Fatalf("二维码内容 = %v，期望 PNG 头", login.QRCode)
	}
	if login.clientType != CookieClientIPad {
		t.Fatalf("会话渠道 = %q，期望 %q", login.clientType, CookieClientIPad)
	}
	if login.uid != "uid-1" || login.issuedAt != "1700000000" || login.sign != "sign-1" {
		t.Fatalf("扫码参数 = %+v", login)
	}
}

// TestBeginCookieLoginRejectsIncompleteToken 验证扫码令牌字段缺失时不产出无法使用的会话。
func TestBeginCookieLoginRejectsIncompleteToken(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":1,"data":{"uid":"uid-1"}}`)
	})
	if _, err := client.BeginCookieLogin(context.Background(), CookieClientWeb); err == nil {
		t.Fatal("缺少扫码参数时应当报错")
	}
}

// TestCookieLoginStatusMapsProviderCodes 验证 Cookie 扫码状态映射，
// 并把 115 用 key invalid 表达的过期响应视为二维码过期而不是上游故障。
func TestCookieLoginStatusMapsProviderCodes(t *testing.T) {
	login := &CookieLogin{uid: "uid-1", issuedAt: "1700000000", sign: "sign-1", clientType: CookieClientWeb}
	cases := []struct {
		body string
		want LoginState
	}{
		{`{"state":1,"data":{"status":0}}`, LoginWaiting},
		{`{"state":1,"data":{"status":1}}`, LoginScanned},
		{`{"state":1,"data":{"status":2}}`, LoginAuthorized},
		{`{"state":1,"data":{"status":-1}}`, LoginExpired},
		{`{"state":1,"data":{"status":-2}}`, LoginCanceled},
	}
	for _, testCase := range cases {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, testCase.body)
		})
		got, err := client.CookieLoginStatus(context.Background(), login)
		if err != nil {
			t.Fatalf("查询 %s 失败: %v", testCase.body, err)
		}
		if got != testCase.want {
			t.Fatalf("状态 %s = %q，期望 %q", testCase.body, got, testCase.want)
		}
	}

	invalid := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":0,"message":"key invalid"}`)
	})
	got, err := invalid.CookieLoginStatus(context.Background(), login)
	if err != nil {
		t.Fatalf("过期响应不应报错: %v", err)
	}
	if got != LoginExpired {
		t.Fatalf("key invalid 状态 = %q，期望 %q", got, LoginExpired)
	}
}

// TestExchangeCookieFormatsAndSorts 验证 Cookie 按名排序拼接、丢弃空项，并由渠道决定换取端点。
func TestExchangeCookieFormatsAndSorts(t *testing.T) {
	var gotPath, gotAccount string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseForm(); err != nil {
			t.Errorf("解析表单失败: %v", err)
		}
		gotAccount = r.PostForm.Get("account")
		writeJSON(t, w, `{"state":true,"data":{"cookie":{"SEID":"seid","UID":"123_abc","CID":"cid","":"","KID":""}}}`)
	})
	login := &CookieLogin{uid: "uid-9", issuedAt: "1", sign: "s", clientType: CookieClientIOS}
	cookie, err := client.ExchangeCookie(context.Background(), login)
	if err != nil {
		t.Fatalf("换取 Cookie 失败: %v", err)
	}
	if gotPath != "/app/1.0/115ios/1.0/login/qrcode/" {
		t.Fatalf("换取端点 = %q", gotPath)
	}
	if gotAccount != "uid-9" {
		t.Fatalf("account 参数 = %q", gotAccount)
	}
	if cookie != "CID=cid; SEID=seid; UID=123_abc" {
		t.Fatalf("Cookie = %q", cookie)
	}
}

// TestExchangeCookieRejectsEmptyPayload 验证 115 未返回 Cookie 时不产出空凭据。
func TestExchangeCookieRejectsEmptyPayload(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":true,"data":{"cookie":{}}}`)
	})
	if _, err := client.ExchangeCookie(context.Background(), &CookieLogin{uid: "u", clientType: CookieClientWeb}); err == nil {
		t.Fatal("空 Cookie 应当报错")
	}
}

// TestRetryDelayClassifiesProviderFailures 固定重试边界：
// 限流对所有方法都重试，网络错误与 5xx 只重试可安全重放的读请求，其余错误一律不重试。
func TestRetryDelayClassifiesProviderFailures(t *testing.T) {
	client := New(nil)
	client.cooldown = 70 * time.Second
	client.backoff = time.Second
	cases := []struct {
		name     string
		method   string
		err      error
		attempt  int
		wantWait time.Duration
		want     bool
	}{
		{
			name:     "业务限流对写操作也重试",
			method:   http.MethodPost,
			err:      &APIError{Message: "已达到当前访问上限"},
			attempt:  1,
			wantWait: 70 * time.Second,
			want:     true,
		},
		{
			name:    "其他业务错误不重试",
			method:  http.MethodGet,
			err:     &APIError{Code: invalidFileIDCode, Message: "参数错误"},
			attempt: 1,
			want:    false,
		},
		{
			name:     "429 无 Retry-After 时按默认冷却",
			method:   http.MethodPost,
			err:      &HTTPError{StatusCode: http.StatusTooManyRequests},
			attempt:  1,
			wantWait: 70 * time.Second,
			want:     true,
		},
		{
			name:     "429 优先采用 Retry-After",
			method:   http.MethodPost,
			err:      &HTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: 3 * time.Second},
			attempt:  1,
			wantWait: 3 * time.Second,
			want:     true,
		},
		{
			name:     "读请求 5xx 按指数退避",
			method:   http.MethodGet,
			err:      &HTTPError{StatusCode: http.StatusServiceUnavailable},
			attempt:  3,
			wantWait: 4 * time.Second,
			want:     true,
		},
		{
			name:    "写请求 5xx 不重试",
			method:  http.MethodPost,
			err:     &HTTPError{StatusCode: http.StatusServiceUnavailable},
			attempt: 1,
			want:    false,
		},
		{
			name:     "读请求网络错误按指数退避",
			method:   http.MethodGet,
			err:      &TransportError{Err: errors.New("连接被重置")},
			attempt:  1,
			wantWait: time.Second,
			want:     true,
		},
		{
			name:    "写请求网络错误不重试",
			method:  http.MethodPost,
			err:     &TransportError{Err: errors.New("连接被重置")},
			attempt: 1,
			want:    false,
		},
		{
			name:    "令牌失效交给上层刷新",
			method:  http.MethodGet,
			err:     ErrUnauthorized,
			attempt: 1,
			want:    false,
		},
		{
			name:    "调用方取消不重试",
			method:  http.MethodGet,
			err:     &TransportError{Err: context.Canceled},
			attempt: 1,
			want:    false,
		},
	}
	for _, testCase := range cases {
		gotWait, got := client.retryDelay(testCase.method, testCase.err, testCase.attempt)
		if got != testCase.want || gotWait != testCase.wantWait {
			t.Errorf("%s：retryDelay = (%v, %v)，期望 (%v, %v)", testCase.name, gotWait, got, testCase.wantWait, testCase.want)
		}
	}
}

// TestParseRetryAfterReadsSecondsAndHTTPDate 验证 Retry-After 的两种合法格式与无效输入。
func TestParseRetryAfterReadsSecondsAndHTTPDate(t *testing.T) {
	if got := parseRetryAfter("30"); got != 30*time.Second {
		t.Errorf("秒数格式 = %v，期望 30s", got)
	}
	if got := parseRetryAfter("0"); got != 0 {
		t.Errorf("零秒 = %v，期望 0", got)
	}
	if got := parseRetryAfter("  "); got != 0 {
		t.Errorf("空值 = %v，期望 0", got)
	}
	if got := parseRetryAfter("not-a-date"); got != 0 {
		t.Errorf("非法值 = %v，期望 0", got)
	}
	future := time.Now().Add(45 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got < 40*time.Second || got > 50*time.Second {
		t.Errorf("HTTP 日期 = %v，期望约 45s", got)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("过期日期 = %v，期望 0", got)
	}
}

// TestExtendCooldownKeepsLatestDeadline 验证并发命中限流时冷却窗口只延长不缩短。
func TestExtendCooldownKeepsLatestDeadline(t *testing.T) {
	client := New(nil)
	client.extendCooldown(80 * time.Millisecond)
	first := client.retryAt
	client.extendCooldown(10 * time.Millisecond)
	if !client.retryAt.Equal(first) {
		t.Fatalf("较短冷却改写了截止时间：%v → %v", first, client.retryAt)
	}
	client.extendCooldown(0)
	if !client.retryAt.Equal(first) {
		t.Fatalf("零冷却不应改写截止时间：%v", client.retryAt)
	}
	client.extendCooldown(120 * time.Millisecond)
	if !client.retryAt.After(first) {
		t.Fatalf("较长冷却未延长截止时间：%v", client.retryAt)
	}
}

// TestCooldownDelaysFollowingRequests 验证冷却窗口对所有链路生效：
// 命中限流后，即使节流间隔为 0，下一次请求也必须等冷却结束。
func TestCooldownDelaysFollowingRequests(t *testing.T) {
	client := New(nil)
	client.gap = 0
	client.playGap = 0
	client.extendCooldown(60 * time.Millisecond)

	start := time.Now()
	release, err := client.admit(context.Background())
	if err != nil {
		t.Fatalf("冷却期间取得请求配额失败: %v", err)
	}
	release()
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("冷却后首次请求等待 %v，期望不小于 60ms", elapsed)
	}

	start = time.Now()
	client.extendCooldown(60 * time.Millisecond)
	if err := client.admitPlay(context.Background()); err != nil {
		t.Fatalf("冷却期间取得直链配额失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("直链换取等待 %v，期望不小于 60ms", elapsed)
	}
}

// TestAdmitStopsOnContextCancelWhileCoolingDown 验证冷却等待可被上下文取消打断，
// 扫描任务取消后不会继续占用并发槽与连接。
func TestAdmitStopsOnContextCancelWhileCoolingDown(t *testing.T) {
	client := New(nil)
	client.gap = 0
	client.extendCooldown(time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := client.admit(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误 = %v，期望 context.DeadlineExceeded", err)
	}
}

// TestAdmitPlayEnforcesMinimumGap 验证直链换取有独立的更小配额，
// 避免播放端批量探测与目录扫描争抢同一份请求窗口。
func TestAdmitPlayEnforcesMinimumGap(t *testing.T) {
	client := New(nil)
	client.playGap = 40 * time.Millisecond
	start := time.Now()
	for index := 0; index < 3; index++ {
		if err := client.admitPlay(context.Background()); err != nil {
			t.Fatalf("取得直链配额失败: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 2*client.playGap {
		t.Fatalf("三次直链换取耗时 %v，期望不小于 %v", elapsed, 2*client.playGap)
	}
}

// TestListRetriesRateLimitResponse 验证 115 用业务错误表达限流时会被识别并重试，
// 而不是把「已达到当前访问上限」直接当成目录扫描失败。
func TestListRetriesRateLimitResponse(t *testing.T) {
	var calls int
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			writeJSON(t, w, `{"state":false,"code":0,"message":"已达到当前访问上限"}`)
			return
		}
		writeJSON(t, w, `{"state":true,"code":0,"count":1,"data":[{"fid":"f1","pid":"0","fn":"a.mkv","fc":"1","fs":10,"pc":"pc1"}],"path":[{"cid":"0","name":"根目录"}]}`)
	})
	page, err := client.List(context.Background(), "token", "0", 0, 100)
	if err != nil {
		t.Fatalf("限流后重试仍失败: %v", err)
	}
	if calls != 2 {
		t.Fatalf("请求次数 = %d，期望 2", calls)
	}
	if len(page.Files) != 1 || page.Files[0].Name != "a.mkv" {
		t.Fatalf("文件列表 = %+v", page.Files)
	}
}

// TestListRetriesServerErrorOnGet 验证可安全重放的读请求在 5xx 时按退避重试。
func TestListRetriesServerErrorOnGet(t *testing.T) {
	var calls int
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeJSON(t, w, `{"state":true,"code":0,"count":0,"data":[],"path":[{"cid":"0","name":"根目录"}]}`)
	})
	page, err := client.List(context.Background(), "token", "0", 0, 100)
	if err != nil {
		t.Fatalf("5xx 后重试仍失败: %v", err)
	}
	if calls != 2 || page.Total != 0 {
		t.Fatalf("请求次数 = %d，总数 = %d，期望 2 / 0", calls, page.Total)
	}
}

// TestListStopsAfterAttemptsExhausted 验证重试次数用尽后返回真实的 115 错误，不静默返回空结果。
// 限流错误的尝试预算远大于普通错误：115 的额度窗口可能持续数十分钟，任务必须能等到恢复。
func TestListStopsAfterAttemptsExhausted(t *testing.T) {
	var calls int
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		writeJSON(t, w, `{"state":false,"code":0,"message":"已达到当前访问上限"}`)
	})
	_, err := client.List(context.Background(), "token", "0", 0, 100)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "已达到当前访问上限") {
		t.Fatalf("错误 = %v，期望携带 115 限流消息的 APIError", err)
	}
	if calls != maxRateLimitAttempts {
		t.Fatalf("限流请求次数 = %d，期望 %d", calls, maxRateLimitAttempts)
	}

	calls = 0
	client = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if _, err := client.List(context.Background(), "token", "0", 0, 100); err == nil {
		t.Fatal("5xx 时应当报错")
	}
	if calls != maxRequestAttempts {
		t.Fatalf("普通错误请求次数 = %d，期望 %d", calls, maxRequestAttempts)
	}
}

// TestRateLimitClassificationCoversCodesAndMessages 固定限流的识别边界：
// 115 的限流既可能以错误码（406 访问上限、770004 频率过高）返回，也可能只有提示文本，
// 两种形式都必须按限流处理，否则扫描任务会被当成普通业务失败而立即终止。
func TestRateLimitClassificationCoversCodesAndMessages(t *testing.T) {
	client := New(nil)
	client.cooldown = time.Minute
	rateLimited := []error{
		&APIError{Code: accessLimitCode, Message: "访问上限"},
		&APIError{Code: requestFrequentCode, Message: "访问频率过高"},
		&APIError{Message: "已达到当前访问上限"},
		&APIError{Message: "请求过于频繁，请稍后再试"},
		&HTTPError{StatusCode: http.StatusTooManyRequests},
		&HTTPError{StatusCode: http.StatusMethodNotAllowed},
	}
	for _, err := range rateLimited {
		if !isRateLimitError(err) {
			t.Errorf("%v 应识别为限流", err)
		}
		if wait, ok := client.retryDelay(http.MethodGet, err, 1); !ok || wait != time.Minute {
			t.Errorf("%v 首次重试 = (%v, %v)，期望 (1m, true)", err, wait, ok)
		}
	}
	if isRateLimitError(&APIError{Code: invalidFileIDCode, Message: "参数错误"}) {
		t.Error("普通业务错误不应识别为限流")
	}
	// 405 是风控阻断页，只在可安全重放的读请求上重试；写请求不重复提交。
	if _, ok := client.retryDelay(http.MethodPost, &HTTPError{StatusCode: http.StatusMethodNotAllowed}, 1); ok {
		t.Error("写请求的 405 不应重试")
	}
}

// TestRateLimitWaitEscalatesAndCaps 验证限流冷却按尝试次数翻倍并封顶，
// 使短暂抖动快速恢复、额度真正用尽时退到足够长的等待。
func TestRateLimitWaitEscalatesAndCaps(t *testing.T) {
	client := New(nil)
	client.cooldown = time.Minute
	wants := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, rateLimitMaxBackoff, rateLimitMaxBackoff}
	for index, want := range wants {
		if got := client.rateLimitWait(index + 1); got != want {
			t.Fatalf("第 %d 次冷却 = %v，期望 %v", index+1, got, want)
		}
	}
	client.cooldown = 0
	if got := client.rateLimitWait(3); got != 0 {
		t.Fatalf("关闭冷却时 = %v，期望 0", got)
	}
}

// TestCooldownReporterReceivesWait 验证限流冷却会通知任务进度，
// 使「等待 115 恢复」可以被展示，而不是表现为任务卡死。
func TestCooldownReporterReceivesWait(t *testing.T) {
	var calls int
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			writeJSON(t, w, `{"state":false,"code":406,"message":"访问上限"}`)
			return
		}
		writeJSON(t, w, `{"state":true,"code":0,"count":"0","data":[]}`)
	})
	// 用毫秒级冷却验证回调内容，避免测试真的等待。
	client.cooldown = 5 * time.Millisecond
	var waits []time.Duration
	ctx := WithCooldownReporter(context.Background(), func(wait time.Duration) { waits = append(waits, wait) })
	if _, err := client.List(ctx, "token", "0", 0, 100); err != nil {
		t.Fatalf("限流后重试失败: %v", err)
	}
	if len(waits) != 1 || waits[0] != 5*time.Millisecond {
		t.Fatalf("冷却回调 = %v，期望 [5ms]", waits)
	}
}

// TestAddOfflineRetriesRateLimitButNotServerError 验证写操作的重试边界：
// 被限流的提交不会在 115 侧生效，可以重试；5xx 可能已经受理，重复提交会产生重复任务，因此不重试。
func TestAddOfflineRetriesRateLimitButNotServerError(t *testing.T) {
	var calls int
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			writeJSON(t, w, `{"state":false,"code":0,"message":"已达到当前访问上限"}`)
			return
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":[{"state":true,"code":0,"info_hash":"hash-1"}]}`)
	})
	hash, err := client.AddOffline(context.Background(), "token", "magnet:?xt=1", "0")
	if err != nil {
		t.Fatalf("限流后重试提交失败: %v", err)
	}
	if hash != "hash-1" || calls != 2 {
		t.Fatalf("哈希 = %q，请求次数 = %d，期望 hash-1 / 2", hash, calls)
	}

	calls = 0
	client = newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if _, err := client.AddOffline(context.Background(), "token", "magnet:?xt=2", "0"); err == nil {
		t.Fatal("5xx 时应当报错")
	}
	if calls != 1 {
		t.Fatalf("5xx 请求次数 = %d，期望 1（写操作不重试）", calls)
	}
}

// TestDownloadURLRetriesRateLimitResponse 验证播放链路同样享受限流退避，不会直接失败。
func TestDownloadURLRetriesRateLimitResponse(t *testing.T) {
	var calls int
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			writeJSON(t, w, `{"state":false,"code":0,"message":"已达到当前访问上限"}`)
			return
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":{"f1":{"url":{"url":"https://cdn.example.com/a.mkv"}}}}`)
	})
	address, err := client.DownloadURL(context.Background(), "token", "pc-1", "UA")
	if err != nil {
		t.Fatalf("限流后重试换取直链失败: %v", err)
	}
	if address != "https://cdn.example.com/a.mkv" || calls != 2 {
		t.Fatalf("直链 = %q，请求次数 = %d，期望 cdn 地址 / 2", address, calls)
	}
}
