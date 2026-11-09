package pan115

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// uploadScalar accepts string/numeric provider identifiers without float conversion.
func uploadScalar(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

// uploadPickCodeID decodes the file-ID portion of the provider's base36 substitution format.
// Unknown versions, directory codes, malformed suffixes and overflowing IDs require API reconciliation.
// Format reference: p115client/modules/p115pickcode; these tables describe the wire encoding.
func uploadPickCodeID(code string) string {
	if len(code) < 6 || strings.Trim(code, "0123456789abcdefghijklmnopqrstuvwxyz") != "" {
		return ""
	}
	var alphabet string
	switch code[0] {
	case 'a':
		alphabet = "fuln1ytpj3smg8d5a094qh7cxkbi62zvewro"
	case 'b':
		alphabet = "sk721n9a0emlfpcrzbqdw3gjh6ty5xui48vo"
	case 'c':
		alphabet = "ywcz3hite6f1j0guoakvdb2ns7p8qr9ml5x4"
	case 'd':
		alphabet = "rq2vl5o7wsken9u8tp4jg3zbyc6xmhifd01a"
	case 'e':
		alphabet = "ljm9eqbcfhw7ktv3x1dgp5ua8y6s4znr2io0"
	default:
		return ""
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if alphabet[strings.IndexByte(digits, code[len(code)-4])] != '0' {
		return ""
	}
	var decoded strings.Builder
	for _, char := range []byte(code[1 : len(code)-4]) {
		decoded.WriteByte(alphabet[strings.IndexByte(digits, char)])
	}
	id, err := strconv.ParseUint(decoded.String(), 36, 64)
	if err != nil || id == 0 {
		return ""
	}
	return strconv.FormatUint(id, 10)
}

// parseUploadCallback checks the business result, not merely OSS transport success.
// Missing identity is returned incomplete for safe lookup; conflicting content is an uncertain upload error.
func parseUploadCallback(body []byte, parent, name string, size int64, hash string) (FileInfo, error) {
	var response struct {
		State *bool `json:"state"`
		Data  struct {
			ID       json.RawMessage `json:"file_id"`
			Name     string          `json:"file_name"`
			Size     json.RawMessage `json:"file_size"`
			SHA1     string          `json:"sha1"`
			PickCode string          `json:"pick_code"`
			Parent   json.RawMessage `json:"parent_id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.State == nil || !*response.State {
		return FileInfo{}, fmt.Errorf("OSS 上传回调未确认业务成功，需核验远端结果")
	}
	d := response.Data
	actualSize, err := strconv.ParseInt(uploadScalar(d.Size), 10, 64)
	if err != nil || actualSize != size || d.Name != name || !strings.EqualFold(d.SHA1, hash) || (uploadScalar(d.Parent) != "" && uploadScalar(d.Parent) != parent) {
		return FileInfo{}, fmt.Errorf("OSS 上传回调内容与源文件不一致，需核验远端结果")
	}
	id := uploadScalar(d.ID)
	if id == "" {
		id = uploadPickCodeID(d.PickCode)
	}
	return FileInfo{File: File{ID: id, ParentID: parent, Name: name, PickCode: d.PickCode, SHA1: d.SHA1, Size: actualSize, SizeKnown: true}}, nil
}
