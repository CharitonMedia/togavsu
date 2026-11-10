package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// emptySHA is the SHA-256 of an empty payload, used for GET, HEAD and DELETE.
const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// S3 talks to S3-compatible storage with path-style requests signed by AWS SigV4.
// ponytail: ceiling: one PUT per object, which S3 caps at 5 GiB, and path-style
// addressing only. Upgrade path: multipart upload and virtual-hosted buckets.
// trigger: files.max_upload_bytes is raised above 5 GiB or a provider answers PermanentRedirect.
type S3 struct {
	Endpoint  *url.URL
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	SecretKey string
	Client    *http.Client
	Now       func() time.Time
}

// NewS3 validates the settings.
func NewS3(s Settings, client *http.Client) (*S3, error) {
	if s.Endpoint == "" || s.Bucket == "" || s.AccessKey == "" || s.SecretKey == "" {
		return nil, fmt.Errorf("%w: s3 needs endpoint, bucket, access_key and secret_key", ErrConfig)
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("%w: s3 endpoint must be an http or https URL", ErrConfig)
	}
	region := s.Region
	if region == "" {
		region = "us-east-1"
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &S3{Endpoint: u, Region: region, Bucket: s.Bucket, Prefix: s.Prefix, AccessKey: s.AccessKey, SecretKey: s.SecretKey, Client: client, Now: time.Now}, nil
}

func (s *S3) objectURL(key string) *url.URL {
	u := *s.Endpoint
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + s.Bucket
	if key != "" {
		u.Path += "/" + s.Prefix + key
	}
	u.RawQuery = ""
	return &u
}

func (s *S3) do(ctx context.Context, method, key string, body io.Reader, size int64, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.objectURL(key).String(), body)
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	payload := emptySHA
	if body != nil {
		payload = "UNSIGNED-PAYLOAD"
		req.ContentLength = size
		if size == zero {
			req.Body = http.NoBody
		}
	}
	Sign(req, s.AccessKey, s.SecretKey, s.Region, "s3", payload, s.Now().UTC())
	return s.Client.Do(req)
}

// Put uploads one object. If-None-Match keeps an existing key from being replaced.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	if size < zero {
		return fmt.Errorf("%w: s3 put needs the size", ErrConfig)
	}
	resp, err := s.do(ctx, http.MethodPut, key, io.NopCloser(r), size, http.Header{"If-None-Match": {"*"}})
	if err != nil {
		return err
	}
	defer drain(resp)
	switch {
	case resp.StatusCode == http.StatusPreconditionFailed || resp.StatusCode == http.StatusConflict:
		return ErrExists
	case resp.StatusCode/100 != http.StatusOK/100:
		return s3Error(resp)
	}
	return nil
}

// Get reads a byte range with a Range header.
func (s *S3) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	if length == zero {
		return io.NopCloser(strings.NewReader("")), nil
	}
	hdr := http.Header{}
	if r := rangeHeader(offset, length); r != "" {
		hdr.Set("Range", r)
	}
	resp, err := s.do(ctx, http.MethodGet, key, nil, zero, hdr)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return resp.Body, nil
	case http.StatusNotFound:
		drain(resp)
		return nil, ErrNotFound
	case http.StatusRequestedRangeNotSatisfiable:
		drain(resp)
		return io.NopCloser(strings.NewReader("")), nil
	default:
		defer drain(resp)
		return nil, s3Error(resp)
	}
}

// Stat issues HEAD.
func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	if err := CheckKey(key); err != nil {
		return Info{}, err
	}
	resp, err := s.do(ctx, http.MethodHead, key, nil, zero, nil)
	if err != nil {
		return Info{}, err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusNotFound {
		return Info{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return Info{}, s3Error(resp)
	}
	return Info{Size: resp.ContentLength}, nil
}

// Delete removes the object. S3 answers 204 for a missing key too.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodDelete, key, nil, zero, nil)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode/100 != http.StatusOK/100 && resp.StatusCode != http.StatusNotFound {
		return s3Error(resp)
	}
	return nil
}

// Health issues HEAD on the bucket.
func (s *S3) Health(ctx context.Context) error {
	resp, err := s.do(ctx, http.MethodHead, "", nil, zero, nil)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return s3Error(resp)
	}
	return nil
}

func rangeHeader(offset, length int64) string {
	if offset <= zero && length < zero {
		return ""
	}
	if length < zero {
		return fmt.Sprintf("bytes=%d-", offset)
	}
	return fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
}

func s3Error(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	return fmt.Errorf("storage: s3 %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}

// Sign adds AWS Signature Version 4 headers to req. The signed headers are host, any
// Range and If-None-Match, and the x-amz headers this function sets.
func Sign(req *http.Request, accessKey, secretKey, region, service, payloadSHA string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadSHA)
	headers := map[string]string{"host": req.URL.Host}
	for _, name := range []string{"Range", "If-None-Match", "X-Amz-Date", "X-Amz-Content-Sha256"} {
		if v := req.Header.Get(name); v != "" {
			headers[strings.ToLower(name)] = strings.TrimSpace(v)
		}
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canon strings.Builder
	for _, k := range names {
		canon.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")
	creq := strings.Join([]string{
		req.Method, canonicalPath(req.URL.EscapedPath()), canonicalQuery(req.URL.Query()),
		canon.String(), signed, payloadSHA,
	}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	sum := sha256.Sum256([]byte(creq))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	k := hmacSHA([]byte("AWS4"+secretKey), day)
	k = hmacSHA(k, region)
	k = hmacSHA(k, service)
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func hmacSHA(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

func canonicalPath(escaped string) string {
	if escaped == "" {
		return "/"
	}
	parts := strings.Split(escaped, "/")
	for i, p := range parts {
		raw, err := url.PathUnescape(p)
		if err != nil {
			raw = p
		}
		parts[i] = awsEscape(raw)
	}
	return strings.Join(parts, "/")
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			out = append(out, awsEscape(k)+"="+awsEscape(v))
		}
	}
	return strings.Join(out, "&")
}

// awsEscape is RFC 3986 percent-encoding with the unreserved set AWS uses.
func awsEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z') || ('0' <= c && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
