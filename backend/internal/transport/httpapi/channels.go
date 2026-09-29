package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/ports"
)

// maxCallbackBytes 限制回调请求体大小，防止异常请求占用内存。
const maxCallbackBytes = 1 << 20

// callbackProcessTimeout 是回调消息后台处理的超时。
// 企业微信要求回调 5 秒内响应，因此这里先返回空响应再异步处理。
const callbackProcessTimeout = 2 * time.Minute

// wechatVerify 处理企业微信回调的 URL 校验：校验签名后原样回写解密出的明文。
func wechatVerify(dependencies Dependencies) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if dependencies.WeChatCallback == nil {
			writeError(response, http.StatusServiceUnavailable, "wechat_not_configured", "企业微信回调未配置")
			return
		}
		echo, err := dependencies.WeChatCallback.Verify(request.URL.Query())
		if err != nil {
			if errors.Is(err, ports.ErrChannelNotConfigured) {
				writeError(response, http.StatusServiceUnavailable, "wechat_not_configured", "企业微信回调未配置")
				return
			}
			logging.Error(logging.CategoryNotification, "企业微信回调校验失败", "error", err.Error())
			writeError(response, http.StatusBadRequest, "wechat_verify_failed", "回调校验失败")
			return
		}
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(echo))
	}
}

// wechatReceive 接收企业微信回调消息：解密后交给渠道处理器，并立即返回空响应。
// 回复通过应用消息接口异步发送，不放在本次响应体里，避免超出企业微信的响应时限。
func wechatReceive(dependencies Dependencies) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if dependencies.WeChatCallback == nil || dependencies.ChannelMessages == nil {
			writeError(response, http.StatusServiceUnavailable, "wechat_not_configured", "企业微信回调未配置")
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, maxCallbackBytes+1))
		if err != nil || len(body) > maxCallbackBytes {
			logging.Error(logging.CategoryNotification, "企业微信回调读取失败")
			writeError(response, http.StatusBadRequest, "wechat_body_invalid", "回调内容无效")
			return
		}
		message, err := dependencies.WeChatCallback.Receive(request.URL.Query(), body)
		if err != nil {
			if errors.Is(err, ports.ErrChannelNotConfigured) {
				writeError(response, http.StatusServiceUnavailable, "wechat_not_configured", "企业微信回调未配置")
				return
			}
			logging.Error(logging.CategoryNotification, "企业微信回调解密失败", "error", err.Error())
			writeError(response, http.StatusBadRequest, "wechat_decrypt_failed", "回调校验失败")
			return
		}
		response.WriteHeader(http.StatusOK)
		// 按钮点击没有文本内容但有动作，两者都为空才视为无需处理的消息。
		if strings.TrimSpace(message.Text) == "" && message.Action == nil {
			return
		}
		handler := dependencies.ChannelMessages
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), callbackProcessTimeout)
			defer cancel()
			if err := handler.Handle(ctx, message); err != nil {
				logging.Error(logging.CategoryAgent, "企业微信消息处理失败", "error", err.Error())
			}
		}()
	}
}
