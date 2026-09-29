package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"bytemuse/backend/internal/application"
)

// testOpenAI 测试当前表单草稿；复用路由鉴权，不保存配置或返回密钥。
func testOpenAI(response http.ResponseWriter, request *http.Request) {
	started := time.Now()
	resultMessage := func(success bool) string {
		status := "失败"
		if success {
			status = "成功"
		}
		return fmt.Sprintf("OpenAI 连接%s (%dms)", status, time.Since(started).Milliseconds())
	}
	var config application.OpenAIConfig
	if err := decodeJSON(response, request, &config); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", resultMessage(false)+"：请求体不是有效的 OpenAI 测试配置")
		return
	}
	if err := application.TestOpenAI(request.Context(), config); err != nil {
		// 应用层错误均为本地生成的脱敏原因，不包含上游正文、URL 或密钥。
		message := resultMessage(false) + "：" + strings.TrimPrefix(err.Error(), "OpenAI ")
		if errors.Is(err, application.ErrInvalidOpenAIConfig) {
			writeError(response, http.StatusBadRequest, "invalid_openai_config", message)
		} else {
			writeError(response, http.StatusBadGateway, "openai_test_failed", message)
		}
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"message": resultMessage(true)})
}
