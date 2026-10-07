package pan115

import (
	"bytemuse/backend/internal/domain"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// uploadProgress translates each OSS part's byte counter into an absolute file counter.
type uploadProgress struct {
	ctx           context.Context
	offset, total int64
}

func (p uploadProgress) ProgressChanged(event *oss.ProgressEvent) {
	if event.EventType == oss.TransferDataEvent {
		domain.ReportUploadProgress(p.ctx, p.offset+event.ConsumedBytes, p.total, "uploading")
	}
}

// CreateFolder creates one child folder using the shared authenticated and throttled OpenAPI client.
func (c *Client) CreateFolder(ctx context.Context, token, parent, name string) (string, error) {
	raw, err := c.apiCall(ctx, http.MethodPost, c.api+"/open/folder/add", token, url.Values{"pid": {parent}, "file_name": {name}}, "创建目录")
	if err != nil {
		return "", err
	}
	var result struct {
		ID string `json:"file_id"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", fmt.Errorf("115 未返回新目录 ID")
	}
	return result.ID, nil
}

// Rename changes only the specified file's name; uncertain failures must be reconciled by the caller.
func (c *Client) Rename(ctx context.Context, token, id, name string) error {
	_, err := c.apiCall(ctx, http.MethodPost, c.api+"/open/ufile/update", token, url.Values{"file_id": {id}, "file_name": {name}}, "重命名")
	return err
}

// Delete moves the exact replaced file to the provider recycle bin; it never clears the recycle bin.
func (c *Client) Delete(ctx context.Context, token, parent, id string) error {
	_, err := c.apiCall(ctx, http.MethodPost, c.api+"/open/ufile/delete", token, url.Values{"parent_id": {parent}, "file_ids": {id}}, "删除已替换文件")
	return err
}

// uploadInit contains provider-issued object-store coordinates, never persisted or logged.
type uploadInit struct {
	Status    int             `json:"status"`
	SignKey   string          `json:"sign_key"`
	SignCheck string          `json:"sign_check"`
	Bucket    string          `json:"bucket"`
	Object    string          `json:"object"`
	Callback  json.RawMessage `json:"callback"`
}

// uploadHash computes an uppercase SHA1 over a bounded section and honors cancellation.
func uploadHash(ctx context.Context, f *os.File, offset, length int64) (string, error) {
	h := sha1.New()
	reader := io.NewSectionReader(f, offset, length)
	buf := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := reader.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return strings.ToUpper(fmt.Sprintf("%x", h.Sum(nil))), nil
}

// Upload uses 115 instant-upload verification followed by bounded OSS multipart transfers when needed.
// The caller owns the open file and must verify the final remote object before reporting completion.
func (c *Client) Upload(ctx context.Context, token, parent, name string, f *os.File, size int64) error {
	hash, err := uploadHash(ctx, f, 0, size)
	if err != nil {
		return err
	}
	pre, err := uploadHash(ctx, f, 0, min(size, 128*1024))
	if err != nil {
		return err
	}
	form := url.Values{"file_name": {name}, "file_size": {strconv.FormatInt(size, 10)}, "target": {"U_1_" + parent}, "fileid": {hash}, "preid": {pre}}
	var init uploadInit
	for attempt := 0; attempt < 2; attempt++ {
		raw, e := c.apiCall(ctx, http.MethodPost, c.api+"/open/upload/init", token, form, "初始化上传")
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &init); e != nil {
			return e
		}
		if init.Status == 2 {
			return nil
		}
		if init.Status != 6 && init.Status != 7 && init.Status != 8 {
			break
		}
		bounds := strings.Split(init.SignCheck, "-")
		if len(bounds) != 2 {
			return fmt.Errorf("115 上传校验范围无效")
		}
		start, e1 := strconv.ParseInt(bounds[0], 10, 64)
		end, e2 := strconv.ParseInt(bounds[1], 10, 64)
		if e1 != nil || e2 != nil || start < 0 || end < start || end >= size {
			return fmt.Errorf("115 上传校验范围越界")
		}
		sign, e := uploadHash(ctx, f, start, end-start+1)
		if e != nil {
			return e
		}
		form.Set("sign_key", init.SignKey)
		form.Set("sign_val", sign)
	}
	if init.Status != 1 || init.Bucket == "" || init.Object == "" {
		return fmt.Errorf("115 未接受上传，状态 %d", init.Status)
	}
	var callback struct {
		Value string `json:"callback"`
		Vars  string `json:"callback_var"`
	}
	if err = json.Unmarshal(init.Callback, &callback); err != nil || callback.Value == "" {
		return fmt.Errorf("115 上传回调无效")
	}
	raw, err := c.apiCall(ctx, http.MethodGet, c.api+"/open/upload/get_token", token, nil, "上传凭据")
	if err != nil {
		return err
	}
	var credentials struct {
		Endpoint string `json:"endpoint"`
		ID       string `json:"AccessKeyId"`
		Secret   string `json:"AccessKeySecret"`
		Token    string `json:"SecurityToken"`
	}
	if err = json.Unmarshal(raw, &credentials); err != nil {
		return err
	}
	endpoint, err := url.Parse(credentials.Endpoint)
	if err != nil || endpoint.Scheme != "https" || !strings.HasSuffix(endpoint.Hostname(), ".aliyuncs.com") {
		return fmt.Errorf("115 返回不可信的上传端点")
	}
	client, err := oss.New(credentials.Endpoint, credentials.ID, credentials.Secret, oss.SecurityToken(credentials.Token))
	if err != nil {
		return err
	}
	bucket, err := client.Bucket(init.Bucket)
	if err != nil {
		return err
	}
	options := []oss.Option{oss.WithContext(ctx), oss.Callback(base64.StdEncoding.EncodeToString([]byte(callback.Value))), oss.CallbackVar(base64.StdEncoding.EncodeToString([]byte(callback.Vars)))}
	if size == 0 {
		return bucket.PutObject(init.Object, io.NewSectionReader(f, 0, 0), options...)
	}
	session, err := bucket.InitiateMultipartUpload(init.Object, oss.WithContext(ctx), oss.Sequential())
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			cleanup, cancel := context.WithTimeout(context.Background(), requestTimeout)
			defer cancel()
			_ = bucket.AbortMultipartUpload(session, oss.WithContext(cleanup))
		}
	}()
	partSize := max(int64(16*1024*1024), (size+9999)/10000)
	parts := []oss.UploadPart{}
	for offset := int64(0); offset < size; offset += partSize {
		count := min(partSize, size-offset)
		part, e := bucket.UploadPart(session, io.NewSectionReader(f, offset, count), count, len(parts)+1, oss.WithContext(ctx), oss.Progress(uploadProgress{ctx, offset, size}))
		if e != nil {
			return e
		}
		parts = append(parts, part)
	}
	_, err = bucket.CompleteMultipartUpload(session, parts, options...)
	complete = err == nil
	return err
}
