package pan115

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// TestInfoMapsInvalidFileIDCode 验证 115 对非法文件标识返回的通用「参数错误」
// 被归一化为 ErrInvalidFileID，避免上层把它当成未知故障。
func TestInfoMapsInvalidFileIDCode(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/open/folder/get_info" {
			t.Errorf("请求路径 = %s，期望 /open/folder/get_info", request.URL.Path)
		}
		writeJSON(t, w, `{"state":false,"code":990002,"message":"参数错误。"}`)
	})
	if _, err := client.Info(context.Background(), "token", "abc"); !errors.Is(err, ErrInvalidFileID) {
		t.Fatalf("Info 错误 = %v，期望 ErrInvalidFileID", err)
	}
}

// TestInfoKeepsOtherProviderErrors 验证其它业务错误码保持原样，不被误判为标识无效。
func TestInfoKeepsOtherProviderErrors(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":false,"code":990001,"message":"服务异常。"}`)
	})
	_, err := client.Info(context.Background(), "token", "123")
	if errors.Is(err, ErrInvalidFileID) {
		t.Fatalf("Info 错误 = %v，不应映射为 ErrInvalidFileID", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 990001 {
		t.Fatalf("Info 错误 = %v，期望保留原始 APIError", err)
	}
}

// TestDownloadURLParsesKeyedData 验证下载接口把结果以文件标识为键直接放在 data 段时能取到直链。
// 115 的 downurl 不在 data 段内再嵌套一层 data，多包一层会得到空结果并误判为「无可用直链」。
func TestDownloadURLParsesKeyedData(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/open/ufile/downurl" {
			t.Errorf("请求路径 = %s，期望 /open/ufile/downurl", request.URL.Path)
		}
		writeJSON(t, w, `{"state":true,"code":0,"data":{"3059333731694149009":{"file_name":"影片.mkv","pick_code":"pc-1","url":{"url":"https://cdn.example.com/a.mkv"}}}}`)
	})
	address, err := client.DownloadURL(context.Background(), "token", "pc-1", "Emby/4.8.0")
	if err != nil {
		t.Fatalf("换取下载地址失败: %v", err)
	}
	if address != "https://cdn.example.com/a.mkv" {
		t.Fatalf("下载地址 = %q", address)
	}
}

// TestDownloadURLReportsUnavailableWhenEmpty 验证 115 未回传直链时返回可识别的错误。
func TestDownloadURLReportsUnavailableWhenEmpty(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"state":true,"code":0,"data":{}}`)
	})
	if _, err := client.DownloadURL(context.Background(), "token", "pc-1", ""); !errors.Is(err, ErrDownloadUnavailable) {
		t.Fatalf("错误 = %v，期望 ErrDownloadUnavailable", err)
	}
}
