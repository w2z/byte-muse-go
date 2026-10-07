package clouddrive

import (
	"errors"
	"fmt"
)

// 本文件是一个最小 protobuf wire 编解码器，只覆盖 CloudDrive2 用到的字段类型。
// CloudDrive2 只提供 gRPC-web 接口，仓库没有 protobuf 工具链，因此这里手写编解码，
// 字段编号严格对应官方 clouddrive.proto v1.0.14；编解码规则由同目录单测固定。

var errProtoTruncated = errors.New("protobuf 数据不完整")

// protoWriter 组装 protobuf 消息；proto3 默认值不写入。
type protoWriter struct{ buf []byte }

func (w *protoWriter) varint(value uint64) {
	for value >= 0x80 {
		w.buf = append(w.buf, byte(value)|0x80)
		value >>= 7
	}
	w.buf = append(w.buf, byte(value))
}

func (w *protoWriter) tag(field, wire int) {
	w.varint(uint64(field)<<3 | uint64(wire))
}

func (w *protoWriter) bytes(field int, value []byte) {
	if len(value) == 0 {
		return
	}
	w.tag(field, 2)
	w.varint(uint64(len(value)))
	w.buf = append(w.buf, value...)
}

func (w *protoWriter) str(field int, value string) { w.bytes(field, []byte(value)) }

func (w *protoWriter) boolean(field int, value bool) {
	if !value {
		return
	}
	w.tag(field, 0)
	w.varint(1)
}

// protoReader 顺序读取一条 protobuf 消息。
type protoReader struct {
	data []byte
	pos  int
}

func (r *protoReader) done() bool { return r.pos >= len(r.data) }

func (r *protoReader) varint() (uint64, error) {
	var value uint64
	var shift uint
	for {
		if r.pos >= len(r.data) {
			return 0, errProtoTruncated
		}
		current := r.data[r.pos]
		r.pos++
		value |= uint64(current&0x7f) << shift
		if current < 0x80 {
			return value, nil
		}
		if shift += 7; shift > 63 {
			return 0, errors.New("protobuf varint 过长")
		}
	}
}

// key 读取字段号与 wire type。
func (r *protoReader) key() (field, wire int, err error) {
	value, err := r.varint()
	if err != nil {
		return 0, 0, err
	}
	return int(value >> 3), int(value & 0x7), nil
}

func (r *protoReader) bytes() ([]byte, error) {
	length, err := r.varint()
	if err != nil {
		return nil, err
	}
	if length > uint64(len(r.data)-r.pos) {
		return nil, errProtoTruncated
	}
	value := r.data[r.pos : r.pos+int(length)]
	r.pos += int(length)
	return value, nil
}

// skip 跳过当前字段，使未知字段不会导致整条消息解码失败。
func (r *protoReader) skip(wire int) error {
	switch wire {
	case 0:
		_, err := r.varint()
		return err
	case 1:
		if r.pos+8 > len(r.data) {
			return errProtoTruncated
		}
		r.pos += 8
		return nil
	case 2:
		_, err := r.bytes()
		return err
	case 5:
		if r.pos+4 > len(r.data) {
			return errProtoTruncated
		}
		r.pos += 4
		return nil
	default:
		return fmt.Errorf("protobuf wire type %d 不受支持", wire)
	}
}

// cloudDriveFile 是 clouddrive.CloudDriveFile 中本服务用到的字段。
type cloudDriveFile struct {
	SHA1         string
	ID           string
	Name         string
	FullPathName string
	Size         int64
	FileType     int
	IsDirectory  bool
}

// downloadURLInfo 是 clouddrive.DownloadUrlPathInfo 中本服务用到的字段。
// Direct 为云盘直链（可能为空，此时用 Path 让 CD2 中转）；Headers 是直链要求的附加请求头。
type downloadURLInfo struct {
	Path      string
	Direct    string
	UserAgent string
	Headers   map[string]string
}

// isDir 判断条目是否为目录。fileType 的 Directory 取值为 0，
// proto3 不会序列化默认值，因此 fileType 缺失即目录；isDirectory 是更新版本补充的显式标记。
func (f cloudDriveFile) isDir() bool { return f.IsDirectory || f.FileType == 0 }

// decodeCloudDriveFile 解码一条 CloudDriveFile；未知字段按 wire type 跳过。
func decodeCloudDriveFile(data []byte) (cloudDriveFile, error) {
	reader := &protoReader{data: data}
	var file cloudDriveFile
	for !reader.done() {
		field, wire, err := reader.key()
		if err != nil {
			return cloudDriveFile{}, err
		}
		switch {
		case field == 70 && wire == 2:
			raw, err := reader.bytes()
			if err != nil {
				return cloudDriveFile{}, err
			}
			hashReader := &protoReader{data: raw}
			var kind uint64
			var hash string
			for !hashReader.done() {
				f, w, e := hashReader.key()
				if e != nil {
					return cloudDriveFile{}, e
				}
				if f == 1 && w == 0 {
					kind, e = hashReader.varint()
				} else if f == 2 && w == 2 {
					var value []byte
					value, e = hashReader.bytes()
					hash = string(value)
				} else {
					e = hashReader.skip(w)
				}
				if e != nil {
					return cloudDriveFile{}, e
				}
			}
			if kind == 2 {
				file.SHA1 = hash
			}
		case field == 1 && wire == 2:
			value, err := reader.bytes()
			if err != nil {
				return cloudDriveFile{}, err
			}
			file.ID = string(value)
		case field == 2 && wire == 2:
			value, err := reader.bytes()
			if err != nil {
				return cloudDriveFile{}, err
			}
			file.Name = string(value)
		case field == 3 && wire == 2:
			value, err := reader.bytes()
			if err != nil {
				return cloudDriveFile{}, err
			}
			file.FullPathName = string(value)
		case field == 4 && wire == 0:
			value, err := reader.varint()
			if err != nil {
				return cloudDriveFile{}, err
			}
			file.Size = int64(value)
		case field == 5 && wire == 0:
			value, err := reader.varint()
			if err != nil {
				return cloudDriveFile{}, err
			}
			file.FileType = int(value)
		case field == 30 && wire == 0:
			value, err := reader.varint()
			if err != nil {
				return cloudDriveFile{}, err
			}
			file.IsDirectory = value != 0
		default:
			if err := reader.skip(wire); err != nil {
				return cloudDriveFile{}, err
			}
		}
	}
	return file, nil
}

// decodeSubFiles 解码 clouddrive.SubFilesReply 的 subFiles 列表。
func decodeSubFiles(data []byte) ([]cloudDriveFile, error) {
	reader := &protoReader{data: data}
	var files []cloudDriveFile
	for !reader.done() {
		field, wire, err := reader.key()
		if err != nil {
			return nil, err
		}
		if field == 1 && wire == 2 {
			raw, err := reader.bytes()
			if err != nil {
				return nil, err
			}
			file, err := decodeCloudDriveFile(raw)
			if err != nil {
				return nil, err
			}
			files = append(files, file)
			continue
		}
		if err := reader.skip(wire); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// decodeDownloadURL 解码 clouddrive.DownloadUrlPathInfo。
func decodeDownloadURL(data []byte) (downloadURLInfo, error) {
	reader := &protoReader{data: data}
	var info downloadURLInfo
	for !reader.done() {
		field, wire, err := reader.key()
		if err != nil {
			return downloadURLInfo{}, err
		}
		switch {
		case (field == 1 || field == 3 || field == 4) && wire == 2:
			value, err := reader.bytes()
			if err != nil {
				return downloadURLInfo{}, err
			}
			switch field {
			case 1:
				info.Path = string(value)
			case 3:
				info.Direct = string(value)
			case 4:
				info.UserAgent = string(value)
			}
		case field == 5 && wire == 2:
			raw, err := reader.bytes()
			if err != nil {
				return downloadURLInfo{}, err
			}
			name, value, err := decodeHeaderEntry(raw)
			if err != nil {
				return downloadURLInfo{}, err
			}
			if name != "" {
				if info.Headers == nil {
					info.Headers = map[string]string{}
				}
				info.Headers[name] = value
			}
		default:
			if err := reader.skip(wire); err != nil {
				return downloadURLInfo{}, err
			}
		}
	}
	return info, nil
}

// decodeHeaderEntry 解码 additionalHeaders 中的一条 key/value。
func decodeHeaderEntry(data []byte) (string, string, error) {
	reader := &protoReader{data: data}
	var name, value string
	for !reader.done() {
		field, wire, err := reader.key()
		if err != nil {
			return "", "", err
		}
		if (field == 1 || field == 2) && wire == 2 {
			raw, err := reader.bytes()
			if err != nil {
				return "", "", err
			}
			if field == 1 {
				name = string(raw)
			} else {
				value = string(raw)
			}
			continue
		}
		if err := reader.skip(wire); err != nil {
			return "", "", err
		}
	}
	return name, value, nil
}

// decodeToken 解码 clouddrive.JWTToken。
func decodeToken(data []byte) (success bool, message, token string, err error) {
	reader := &protoReader{data: data}
	for !reader.done() {
		field, wire, err := reader.key()
		if err != nil {
			return false, "", "", err
		}
		switch {
		case field == 1 && wire == 0:
			value, err := reader.varint()
			if err != nil {
				return false, "", "", err
			}
			success = value != 0
		case (field == 2 || field == 3) && wire == 2:
			raw, err := reader.bytes()
			if err != nil {
				return false, "", "", err
			}
			if field == 2 {
				message = string(raw)
			} else {
				token = string(raw)
			}
		default:
			if err := reader.skip(wire); err != nil {
				return false, "", "", err
			}
		}
	}
	return success, message, token, nil
}

// encodeGetTokenRequest 编码 clouddrive.GetTokenRequest。
func encodeGetTokenRequest(username, password string) []byte {
	writer := &protoWriter{}
	writer.str(1, username)
	writer.str(2, password)
	return writer.buf
}

// encodeListSubFileRequest 编码 clouddrive.ListSubFileRequest，并强制刷新缓存。
func encodeListSubFileRequest(path string) []byte {
	writer := &protoWriter{}
	writer.str(1, path)
	writer.boolean(2, true)
	return writer.buf
}

// encodeDownloadURLRequest 编码 clouddrive.GetDownloadUrlPathRequest；
// get_direct_url 为真时要求返回云盘直链，避免流量绕经 CloudDrive2 服务端。
func encodeDownloadURLRequest(path string, direct bool) []byte {
	writer := &protoWriter{}
	writer.str(1, path)
	writer.boolean(4, direct)
	return writer.buf
}

// encodeCreateFolderRequest 编码 clouddrive.CreateFolderRequest。
func encodeCreateFolderRequest(parentPath, folderName string) []byte {
	writer := &protoWriter{}
	writer.str(1, parentPath)
	writer.str(2, folderName)
	return writer.buf
}

// decodeFileOperationResult 解码 clouddrive.FileOperationResult。
func decodeFileOperationResult(data []byte) (success bool, message string, err error) {
	reader := &protoReader{data: data}
	for !reader.done() {
		field, wire, err := reader.key()
		if err != nil {
			return false, "", err
		}
		switch {
		case field == 1 && wire == 0:
			value, err := reader.varint()
			if err != nil {
				return false, "", err
			}
			success = value != 0
		case field == 2 && wire == 2:
			raw, err := reader.bytes()
			if err != nil {
				return false, "", err
			}
			message = string(raw)
		default:
			if err := reader.skip(wire); err != nil {
				return false, "", err
			}
		}
	}
	return success, message, nil
}

// decodeCreateFolderResult 解码 clouddrive.CreateFolderResult。
// folderCreated 存在即视为创建成功；result.success 是旧版本的兜底判定。
func decodeCreateFolderResult(data []byte) (created bool, message string, err error) {
	reader := &protoReader{data: data}
	var succeeded bool
	for !reader.done() {
		field, wire, err := reader.key()
		if err != nil {
			return false, "", err
		}
		switch {
		case field == 1 && wire == 2:
			if _, err := reader.bytes(); err != nil {
				return false, "", err
			}
			created = true
		case field == 2 && wire == 2:
			raw, err := reader.bytes()
			if err != nil {
				return false, "", err
			}
			ok, text, err := decodeFileOperationResult(raw)
			if err != nil {
				return false, "", err
			}
			succeeded, message = ok, text
		default:
			if err := reader.skip(wire); err != nil {
				return false, "", err
			}
		}
	}
	return created || succeeded, message, nil
}
