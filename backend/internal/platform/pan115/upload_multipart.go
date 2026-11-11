package pan115

import (
	"bytemuse/backend/internal/domain"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// ErrUploadSessionMissing requires the caller to reconcile the completed file before restarting.
// OSS removes a session after completion too, so NoSuchUpload alone never authorizes a new upload.
var ErrUploadSessionMissing = errors.New("OSS 上传会话不存在，必须先核对完成结果")

const uploadIdleTimeout = 2 * time.Minute

// uploadPartSize is the minimum multipart size and the single-PUT size limit.
// Existing multipart sessions always retain their original transfer mode.
const uploadPartSize int64 = 16 * 1024 * 1024

// uploadIdleConn bounds each network read/write, allowing long transfers while bytes keep moving.
type uploadIdleConn struct {
	net.Conn
	idle time.Duration
}

func (c uploadIdleConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(p)
	if n > 0 {
		_ = c.Conn.SetDeadline(time.Now().Add(c.idle))
	}
	return n, err
}

func (c uploadIdleConn) Write(p []byte) (int, error) {
	if err := c.Conn.SetDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Write(p)
	if n > 0 {
		// HTTP may already be waiting for response bytes while the request body is
		// still uploading. Progress in either direction renews both deadlines.
		_ = c.Conn.SetDeadline(time.Now().Add(c.idle))
	}
	return n, err
}

// uploadHTTPClient preserves proxy/TLS settings and bounds stalls without limiting total transfer time.
// Opaque custom RoundTrippers cannot expose socket activity, so they receive a per-request fallback.
func uploadHTTPClient(base http.RoundTripper, idle time.Duration) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	client := &http.Client{Transport: base, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	transport, ok := base.(*http.Transport)
	if !ok {
		client.Timeout = 30 * time.Minute
		return client
	}
	clone := transport.Clone()
	dial := clone.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: idle, KeepAlive: 30 * time.Second}).DialContext
	}
	clone.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return uploadIdleConn{conn, idle}, nil
	}
	clone.ResponseHeaderTimeout = idle
	clone.TLSHandshakeTimeout = idle
	// Custom TLS dialing bypasses DialContext; retain it with a bounded request fallback.
	if clone.DialTLSContext != nil || clone.DialTLS != nil {
		client.Timeout = 30 * time.Minute
	}
	client.Transport = clone
	return client
}

// uploadServiceCode handles both value and pointer SDK errors without parsing provider messages.
func uploadServiceCode(err error, code string) bool {
	var value oss.ServiceError
	if errors.As(err, &value) {
		return value.Code == code
	}
	var pointer *oss.ServiceError
	return errors.As(err, &pointer) && pointer != nil && pointer.Code == code
}

// uploadBucket always fetches fresh short-lived keys and rejects endpoints outside the provider domain.
// Socket inactivity is bounded independently of the ordinary API timeout.
func (c *Client) uploadBucket(ctx context.Context, token, name string) (*oss.Bucket, error) {
	raw, err := c.apiCall(ctx, http.MethodGet, c.api+"/open/upload/get_token", token, nil, "上传凭据")
	if err != nil {
		return nil, err
	}
	var credentials struct {
		Endpoint string `json:"endpoint"`
		ID       string `json:"AccessKeyId"`
		Secret   string `json:"AccessKeySecret"`
		Token    string `json:"SecurityToken"`
	}
	if err = json.Unmarshal(raw, &credentials); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(credentials.Endpoint)
	if err != nil || endpoint.Scheme != "https" || !strings.HasSuffix(endpoint.Hostname(), ".aliyuncs.com") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") || (endpoint.Port() != "" && endpoint.Port() != "443") {
		return nil, fmt.Errorf("115 返回不可信的上传端点")
	}
	if credentials.ID == "" || credentials.Secret == "" || credentials.Token == "" {
		return nil, fmt.Errorf("115 返回不完整的上传凭据")
	}
	client, err := oss.New(credentials.Endpoint, credentials.ID, credentials.Secret, oss.SecurityToken(credentials.Token), oss.HTTPClient(uploadHTTPClient(c.http.Transport, uploadIdleTimeout)))
	if err != nil {
		return nil, err
	}
	return client.Bucket(name)
}

// uploadOSS retries only the rejected operation once after STS expiry; network ambiguity is returned intact.
func (c *Client) uploadOSS(ctx context.Context, token, name string, bucket **oss.Bucket, operation func(*oss.Bucket) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := operation(*bucket)
	if !isUploadCredentialExpired(err) {
		return err
	}
	fresh, err := c.uploadBucket(ctx, token, name)
	if err != nil {
		return err
	}
	*bucket = fresh
	return operation(fresh)
}

// validateMultipartBinding prevents a durable session from being reused for a different source or destination.
func validateMultipartBinding(s domain.UploadMultipart, journal *domain.UploadMultipartJournal, parent, name, hash string, size int64) error {
	if s.Account == "" || s.Account != journal.Account || s.Parent != parent || s.Name != name || !strings.EqualFold(s.SHA1, hash) || s.Size != size || s.Modified != journal.Modified {
		return fmt.Errorf("OSS 上传会话与当前账号、目标或源文件版本不一致")
	}
	if s.Bucket == "" || s.Object == "" || s.UploadID == "" || s.Callback == "" || s.Size <= 0 || s.PartSize <= 0 || (s.Size-1)/s.PartSize+1 > 10000 {
		return fmt.Errorf("OSS 上传会话数据无效")
	}
	return nil
}

// multipartCoordinates preserves provider-issued identity across credential refreshes and process restarts.
func multipartCoordinates(s domain.UploadMultipart) oss.InitiateMultipartUploadResult {
	return oss.InitiateMultipartUploadResult{Bucket: s.Bucket, Key: s.Object, UploadID: s.UploadID}
}

// listMultipartParts validates every page before any additional bytes are sent or a completion is attempted.
func (c *Client) listMultipartParts(ctx context.Context, token string, s domain.UploadMultipart, bucket **oss.Bucket) (map[int]oss.UploadPart, int64, error) {
	parts := make(map[int]oss.UploadPart)
	var uploaded int64
	marker := 0
	count := int((s.Size-1)/s.PartSize + 1)
	for {
		var page oss.ListUploadedPartsResult
		err := c.uploadOSS(ctx, token, s.Bucket, bucket, func(b *oss.Bucket) error {
			var e error
			page, e = b.ListUploadedParts(multipartCoordinates(s), oss.WithContext(ctx), oss.PartNumberMarker(marker), oss.MaxParts(1000))
			return e
		})
		if uploadServiceCode(err, "NoSuchUpload") {
			return nil, 0, ErrUploadSessionMissing
		}
		if err != nil {
			return nil, 0, err
		}
		if page.Bucket != s.Bucket || page.Key != s.Object || page.UploadID != s.UploadID {
			return nil, 0, fmt.Errorf("OSS 分片列表会话标识不匹配")
		}
		last := marker
		for _, part := range page.UploadedParts {
			if part.PartNumber <= last || part.PartNumber > count || strings.TrimSpace(part.ETag) == "" {
				return nil, 0, fmt.Errorf("OSS 分片编号或 ETag 无效")
			}
			expected := min(s.PartSize, s.Size-int64(part.PartNumber-1)*s.PartSize)
			if int64(part.Size) != expected {
				return nil, 0, fmt.Errorf("OSS 分片大小与源文件不一致")
			}
			parts[part.PartNumber] = oss.UploadPart{PartNumber: part.PartNumber, ETag: part.ETag}
			uploaded += expected
			last = part.PartNumber
		}
		if !page.IsTruncated {
			return parts, uploaded, nil
		}
		next, err := strconv.Atoi(page.NextPartNumberMarker)
		if err != nil || next <= marker || next != last || next >= count {
			return nil, 0, fmt.Errorf("OSS 分片分页游标无效")
		}
		marker = next
	}
}

// uploadMultipart uses a single PUT for new small files and persists multipart sessions before sending large files.
// Existing sessions always resume. Only verified callback data produces a receipt; the caller owns journal cleanup.
func (c *Client) uploadMultipart(ctx context.Context, token string, f *os.File, s domain.UploadMultipart, resume bool, journal *domain.UploadMultipartJournal, receipt *FileInfo) error {
	bucket, err := c.uploadBucket(ctx, token, s.Bucket)
	if err != nil {
		return err
	}
	var callbackBody []byte
	options := []oss.Option{oss.WithContext(ctx), oss.Callback(base64.StdEncoding.EncodeToString([]byte(s.Callback))), oss.CallbackVar(base64.StdEncoding.EncodeToString([]byte(s.CallbackVar))), oss.CallbackResult(&callbackBody)}
	finish := func() error {
		result, err := parseUploadCallback(callbackBody, s.Parent, s.Name, s.Size, s.SHA1)
		if err == nil && receipt != nil {
			*receipt = result
		}
		return err
	}
	// Preserve absolute progress across credential retries for both transfer modes.
	outerCtx := ctx
	var highWater int64
	ctx = domain.WithUploadProgress(ctx, func(done, total int64, phase string) {
		if done < highWater {
			return
		}
		highWater = done
		domain.ReportUploadProgress(outerCtx, done, total, phase)
	})
	// p115client uses a single PUT when multipart is not explicitly requested.
	// New small files need no multipart initialization; existing journals still resume below.
	if !resume && s.Size <= uploadPartSize {
		putOptions := append(options, oss.Progress(uploadProgress{ctx, 0, s.Size}))
		err = c.uploadOSS(ctx, token, s.Bucket, &bucket, func(b *oss.Bucket) error {
			return b.PutObject(s.Object, io.NewSectionReader(f, 0, s.Size), putOptions...)
		})
		if err != nil {
			return fmt.Errorf("OSS 单请求上传失败：%w", err)
		}
		return finish()
	}
	if !resume {
		var initiated oss.InitiateMultipartUploadResult
		err = c.uploadOSS(ctx, token, s.Bucket, &bucket, func(b *oss.Bucket) error {
			var e error
			// p115client sends sequential=1 when creating the multipart session.
			// Preserve that request shape without assuming it grants multipart permission.
			initiated, e = b.InitiateMultipartUpload(s.Object, oss.WithContext(ctx), oss.AddParam("sequential", "1"))
			return e
		})
		if err != nil {
			return fmt.Errorf("OSS 初始化分片失败：%w", err)
		}
		if initiated.UploadID == "" || initiated.Bucket != s.Bucket || initiated.Key != s.Object {
			return fmt.Errorf("OSS 上传会话初始化响应无效")
		}
		s.UploadID, s.CreatedAt = initiated.UploadID, time.Now().Unix()
		if journal != nil {
			// Expose even an uncommitted identity so the owning task can retain it on error.
			journal.Session = &s
			if err = journal.Save(ctx, &s); err != nil {
				// Cancellation can race initiation. Bound recovery independently, and send no file bytes.
				recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
				defer cancel()
				if saveErr := journal.Save(recovery, &s); saveErr != nil {
					cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
					defer stop()
					return errors.Join(err, saveErr, c.AbortUpload(cleanup, token, s))
				}
				return err
			}
		}
	}
	complete := false
	defer func() {
		if journal == nil && !complete {
			cleanup, cancel := context.WithTimeout(context.Background(), requestTimeout)
			defer cancel()
			_ = c.AbortUpload(cleanup, token, s)
		}
	}()
	parts := make(map[int]oss.UploadPart)
	var transferred int64
	if resume {
		parts, transferred, err = c.listMultipartParts(ctx, token, s, &bucket)
		if err != nil {
			return fmt.Errorf("OSS 查询已上传分片失败：%w", err)
		}
	}
	domain.ReportUploadProgress(ctx, transferred, s.Size, "uploading")
	count := int((s.Size-1)/s.PartSize + 1)
	ordered := make([]oss.UploadPart, 0, count)
	for number := 1; number <= count; number++ {
		part, exists := parts[number]
		if !exists {
			offset := int64(number-1) * s.PartSize
			length := min(s.PartSize, s.Size-offset)
			err = c.uploadOSS(ctx, token, s.Bucket, &bucket, func(b *oss.Bucket) error {
				var e error
				part, e = b.UploadPart(multipartCoordinates(s), io.NewSectionReader(f, offset, length), length, number, oss.WithContext(ctx), oss.Progress(uploadProgress{ctx, transferred, s.Size}))
				return e
			})
			if uploadServiceCode(err, "NoSuchUpload") {
				return ErrUploadSessionMissing
			}
			if err != nil {
				return fmt.Errorf("OSS 上传第 %d 个分片失败：%w", number, err)
			}
			if strings.TrimSpace(part.ETag) == "" {
				return fmt.Errorf("OSS 上传分片未返回 ETag")
			}
			transferred += length
			domain.ReportUploadProgress(ctx, transferred, s.Size, "uploading")
		}
		ordered = append(ordered, part)
	}
	err = c.uploadOSS(ctx, token, s.Bucket, &bucket, func(b *oss.Bucket) error {
		var e error
		// p115client explicitly declares the XML payload type on completion.
		completeOptions := append([]oss.Option{}, options...)
		completeOptions = append(completeOptions, oss.ContentType("text/xml"))
		_, e = b.CompleteMultipartUpload(multipartCoordinates(s), ordered, completeOptions...)
		return e
	})
	if uploadServiceCode(err, "NoSuchUpload") {
		return ErrUploadSessionMissing
	}
	if err != nil {
		return fmt.Errorf("OSS 完成分片失败：%w", err)
	}
	complete = true
	return finish()
}

// AbortUpload removes only the exact unfinished multipart session; completed cloud files are never deleted.
// NoSuchUpload is idempotent success. The caller must retain its journal until this method succeeds.
func (c *Client) AbortUpload(ctx context.Context, token string, s domain.UploadMultipart) error {
	if s.Bucket == "" || s.Object == "" || s.UploadID == "" {
		return fmt.Errorf("OSS 上传会话标识无效")
	}
	bucket, err := c.uploadBucket(ctx, token, s.Bucket)
	if err != nil {
		return err
	}
	err = c.uploadOSS(ctx, token, s.Bucket, &bucket, func(b *oss.Bucket) error {
		return b.AbortMultipartUpload(multipartCoordinates(s), oss.WithContext(ctx))
	})
	if uploadServiceCode(err, "NoSuchUpload") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("OSS 取消分片失败：%w", err)
	}
	return nil
}
