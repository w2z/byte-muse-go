package clouddrive

import (
	"encoding/binary"
	"strings"
	"testing"
)

// cloudDriveFilePayload 按官方字段号组装一条 CloudDriveFile，用于固定解码规则。
func cloudDriveFilePayload(id, name, fullPath string, size int64, fileType int, isDir bool) []byte {
	writer := &protoWriter{}
	writer.str(1, id)
	writer.str(2, name)
	writer.str(3, fullPath)
	if size != 0 {
		writer.tag(4, 0)
		writer.varint(uint64(size))
	}
	if fileType != 0 {
		writer.tag(5, 0)
		writer.varint(uint64(fileType))
	}
	writer.boolean(30, isDir)
	return writer.buf
}

// frame 组装一个 gRPC-web 帧。
func frame(flag byte, payload []byte) []byte {
	out := make([]byte, frameHeaderBytes+len(payload))
	out[0] = flag
	binary.BigEndian.PutUint32(out[1:frameHeaderBytes], uint32(len(payload)))
	copy(out[frameHeaderBytes:], payload)
	return out
}

// TestDecodeSubFiles 验证目录与文件的类型判定：proto3 不序列化默认值，
// 因此 fileType 缺失即目录，显式 isDirectory 也视为目录。
func TestDecodeSubFiles(t *testing.T) {
	reply := &protoWriter{}
	reply.bytes(1, cloudDriveFilePayload("1", "115", "/115", 0, 0, true))
	reply.bytes(1, cloudDriveFilePayload("2", "a.mkv", "/115/a.mkv", 1024, 1, false))

	files, err := decodeSubFiles(reply.buf)
	if err != nil {
		t.Fatalf("解码目录列表失败: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("条目数量不符: %d", len(files))
	}
	if !files[0].isDir() || files[0].Name != "115" || files[0].FullPathName != "/115" {
		t.Fatalf("目录条目解码不符: %+v", files[0])
	}
	if files[1].isDir() || files[1].Size != 1024 {
		t.Fatalf("文件条目解码不符: %+v", files[1])
	}
}

// TestDecodeDownloadURL 验证直链、User-Agent 与附加请求头都能还原。
func TestDecodeDownloadURL(t *testing.T) {
	header := &protoWriter{}
	header.str(1, "X-Token")
	header.str(2, "abc")

	info := &protoWriter{}
	info.str(1, "/115/a.mkv")
	info.str(3, "https://direct.example/a.mkv")
	info.str(4, "cd2-agent")
	info.bytes(5, header.buf)

	decoded, err := decodeDownloadURL(info.buf)
	if err != nil {
		t.Fatalf("解码下载地址失败: %v", err)
	}
	if decoded.Path != "/115/a.mkv" || decoded.Direct != "https://direct.example/a.mkv" {
		t.Fatalf("下载地址解码不符: %+v", decoded)
	}
	if decoded.UserAgent != "cd2-agent" || decoded.Headers["X-Token"] != "abc" {
		t.Fatalf("直链请求头解码不符: %+v", decoded)
	}
}

// TestDecodeToken 验证登录结果的成败与令牌还原。
func TestDecodeToken(t *testing.T) {
	ok := &protoWriter{}
	ok.boolean(1, true)
	ok.str(3, "jwt-token")
	success, message, token, err := decodeToken(ok.buf)
	if err != nil || !success || token != "jwt-token" || message != "" {
		t.Fatalf("成功令牌解码不符: %v %v %q %q", success, message, token, err)
	}

	failed := &protoWriter{}
	failed.str(2, "账号或密码不正确")
	success, message, token, err = decodeToken(failed.buf)
	if err != nil || success || token != "" || message != "账号或密码不正确" {
		t.Fatalf("失败令牌解码不符: %v %v %q %q", success, message, token, err)
	}
}

// TestEncodedRequests 固定各请求消息的字段号，字段漂移会直接导致 CloudDrive2 拒绝调用。
func TestEncodedRequests(t *testing.T) {
	check := func(name string, payload []byte, want map[int]string) {
		t.Helper()
		reader := &protoReader{data: payload}
		got := map[int]string{}
		for !reader.done() {
			field, wire, err := reader.key()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if wire != 2 {
				if _, err := reader.varint(); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				continue
			}
			value, err := reader.bytes()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got[field] = string(value)
		}
		for field, expected := range want {
			if got[field] != expected {
				t.Fatalf("%s: 字段 %d 为 %q，期望 %q", name, field, got[field], expected)
			}
		}
	}
	check("GetToken", encodeGetTokenRequest("admin", "secret"), map[int]string{1: "admin", 2: "secret"})
	check("ListSubFile", encodeListSubFileRequest("/115"), map[int]string{1: "/115"})
	check("GetDownloadUrlPath", encodeDownloadURLRequest("/115/a.mkv", true), map[int]string{1: "/115/a.mkv"})
	check("CreateFolder", encodeCreateFolderRequest("/115", "影片"), map[int]string{1: "/115", 2: "影片"})
}

// TestDecodeCreateFolderResult 覆盖新旧两种成功判定与失败原因。
func TestDecodeCreateFolderResult(t *testing.T) {
	created := &protoWriter{}
	// 官方 proto：CreateFolderResult.folderCreated 是 CloudDriveFile 消息，存在即视为创建成功。
	created.bytes(1, cloudDriveFilePayload("1", "影片", "/115/影片", 0, 0, true))
	ok, message, err := decodeCreateFolderResult(created.buf)
	if err != nil || !ok || message != "" {
		t.Fatalf("创建成功解码不符: %v %q %v", ok, message, err)
	}

	legacy := &protoWriter{}
	result := &protoWriter{}
	result.boolean(1, true)
	legacy.bytes(2, result.buf)
	if ok, _, err = decodeCreateFolderResult(legacy.buf); err != nil || !ok {
		t.Fatalf("旧版结果应视为创建成功: %v %v", ok, err)
	}

	failed := &protoWriter{}
	reason := &protoWriter{}
	reason.str(2, "目录已存在")
	failed.bytes(2, reason.buf)
	if ok, message, err = decodeCreateFolderResult(failed.buf); err != nil || ok || message != "目录已存在" {
		t.Fatalf("创建失败解码不符: %v %q %v", ok, message, err)
	}
}

// TestGrpcWebFrames 验证数据帧解析与 trailer 中的错误状态。
func TestGrpcWebFrames(t *testing.T) {
	payload := []byte{0x0a, 0x03, 'a', 'b', 'c'}
	body := append(frame(0, payload), frame(0x80, []byte("grpc-status: 0\r\n"))...)
	frames, err := grpcWebFrames(body)
	if err != nil {
		t.Fatalf("解析数据帧失败: %v", err)
	}
	if len(frames) != 1 || string(frames[0]) != string(payload) {
		t.Fatalf("数据帧内容不符: %q", frames)
	}

	failed := frame(0x80, []byte("grpc-status: 5\r\ngrpc-message: %E6%97%A0%E6%9D%83%E9%99%90\r\n"))
	if _, err = grpcWebFrames(failed); err == nil || !strings.Contains(err.Error(), "无权限") {
		t.Fatalf("非零 grpc-status 应报错并还原消息: %v", err)
	}

	if _, err = grpcWebFrames([]byte{0x00, 0x00}); err == nil {
		t.Fatalf("截断帧应报错: %v", err)
	}
	if _, err = grpcWebFrames(frame(0, []byte{1, 2, 3, 4, 5})[:4]); err == nil {
		t.Fatal("帧长度超出响应体时应报错")
	}
}
