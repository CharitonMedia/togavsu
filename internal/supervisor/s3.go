package supervisor

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// S3Destination copies archives to S3-compatible storage with path-style
// requests and AWS Signature Version 4.
//
// ponytail: path-style SigV4 only, ceiling: no virtual-hosted buckets and a
// buffered upload when the checksum is not already known, upgrade: virtual-hosted
// addressing and aws-chunked streaming, trigger: a destination test returns
// PermanentRedirect or a backup copy exceeds 512 MiB.
type S3Destination struct {
	Spec DestinationSpec
	HTTP *http.Client
	Now  func() time.Time
}

func (s *S3Destination) ID() string   { return s.Spec.ID }
func (s *S3Destination) Kind() string { return "s3" }

func (s *S3Destination) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}

func (s *S3Destination) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *S3Destination) Put(ctx context.Context, name string, r io.Reader, sha string) error {
	key := s.objectKey(name)
	if seeker, ok := r.(io.ReadSeeker); ok && sha != "" {
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return err
		}
		res, err := s.do(ctx, http.MethodPut, key, nil, seeker, sha)
		if err != nil {
			return err
		}
		res.Body.Close()
		return nil
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if sha != "" && got != sha {
		return ErrChecksum
	}
	res, err := s.do(ctx, http.MethodPut, key, nil, bytes.NewReader(body), got)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func (s *S3Destination) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	res, err := s.do(ctx, http.MethodGet, s.objectKey(name), nil, nil, emptySHA256)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

func (s *S3Destination) Delete(ctx context.Context, name string) error {
	res, err := s.do(ctx, http.MethodDelete, s.objectKey(name), nil, nil, emptySHA256)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func (s *S3Destination) List(ctx context.Context) ([]RemoteObject, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	if prefix := strings.Trim(s.objectPrefix(), "/"); prefix != "" {
		q.Set("prefix", prefix)
	}
	res, err := s.do(ctx, http.MethodGet, "", q, nil, emptySHA256)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var parsed struct {
		Contents []struct {
			Key          string `xml:"Key"`
			LastModified string `xml:"LastModified"`
		} `xml:"Contents"`
	}
	if err := xml.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	var out []RemoteObject
	for _, item := range parsed.Contents {
		when, err := time.Parse(time.RFC3339, item.LastModified)
		if err != nil {
			when = time.Time{}
		}
		name := strings.TrimPrefix(item.Key, s.objectPrefix())
		out = append(out, RemoteObject{Name: name, Created: when})
	}
	return out, nil
}

func (s *S3Destination) Test(ctx context.Context) error {
	return roundTripTest(ctx, s)
}

func (s *S3Destination) objectPrefix() string {
	var parts []string
	if p := strings.Trim(s.Spec.Prefix, "/"); p != "" {
		parts = append(parts, p)
	}
	if s.Spec.Namespace != "" {
		parts = append(parts, s.Spec.Namespace)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "/") + "/"
}

func (s *S3Destination) objectKey(name string) string {
	return s.objectPrefix() + name
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func (s *S3Destination) do(ctx context.Context, method, key string, query url.Values, body io.Reader, payloadHash string) (*http.Response, error) {
	if key != "" && !strings.HasPrefix(key, s.objectPrefix()) {
		return nil, ErrBadRequest
	}
	name := strings.TrimPrefix(key, s.objectPrefix())
	if name != "" {
		if err := safeName(name); err != nil {
			return nil, err
		}
	}
	endpoint, err := url.Parse(s.Spec.Endpoint)
	if err != nil {
		return nil, err
	}
	// Path stays raw. RawPath is the SigV4 canonical URI. Go sends RawPath
	// only when PathUnescape(RawPath) equals Path, so both must be set from
	// the same raw key and the encoder must run only once.
	rawPath := "/" + s.Spec.Bucket + "/"
	if key != "" {
		rawPath += key
	}
	canonPath := canonicalPath(rawPath)
	u := *endpoint
	u.Path = rawPath
	u.RawPath = canonPath
	u.RawQuery = canonicalQuery(query)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.URL.Path = rawPath
	req.URL.RawPath = canonPath
	req.URL.RawQuery = u.RawQuery
	if seeker, ok := body.(io.ReadSeeker); ok {
		req.GetBody = func() (io.ReadCloser, error) {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
			return io.NopCloser(seeker), nil
		}
	}
	if f, ok := body.(*os.File); ok {
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		pos, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		req.ContentLength = info.Size() - pos
	}
	amz := s.now().UTC().Format("20060102T150405Z")
	req.Host = u.Host
	req.Header.Set("Host", u.Host)
	req.Header.Set("x-amz-date", amz)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	signS3(req, payloadHash, s.Spec.AccessKey, s.Spec.SecretKey, s.Spec.Region, "s3", amz)
	res, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		return nil, ErrNotFound
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		return nil, fmt.Errorf("s3 %s: %s", res.Status, Scrub(string(msg)))
	}
	return res, nil
}

func signS3(req *http.Request, payloadHash, access, secret, region, service, amz string) {
	date := amz[:8]
	signed := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	var canonHeaders strings.Builder
	for _, h := range signed {
		canonHeaders.WriteString(h)
		canonHeaders.WriteString(":")
		canonHeaders.WriteString(strings.TrimSpace(req.Header.Get(http.CanonicalHeaderKey(h))))
		canonHeaders.WriteString("\n")
	}
	canon := strings.Join([]string{
		req.Method,
		requestCanonicalURI(req.URL),
		req.URL.RawQuery,
		canonHeaders.String(),
		strings.Join(signed, ";"),
		payloadHash,
	}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amz + "\n" + scope + "\n" + hex.EncodeToString(hash256([]byte(canon)))
	key := hmacSHA([]byte("AWS4"+secret), date)
	key = hmacSHA(key, region)
	key = hmacSHA(key, service)
	key = hmacSHA(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(key, stringToSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+access+"/"+scope+", SignedHeaders="+strings.Join(signed, ";")+", Signature="+sig)
}

// requestCanonicalURI is the path that will be sent. When RawPath is a valid
// encoding of Path, Go's client sends RawPath and the signature must use that
// exact string. Otherwise the raw path is encoded once.
func requestCanonicalURI(u *url.URL) string {
	if u == nil {
		return "/"
	}
	if u.RawPath != "" && u.EscapedPath() == u.RawPath {
		return u.RawPath
	}
	return canonicalPath(u.Path)
}

// canonicalPath encodes each raw segment and keeps every '/' separator.
// It does not drop empty, ".", or ".." segments and does not reorder them.
func canonicalPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = awsURIEncode(part, false)
	}
	return strings.Join(parts, "/")
}

func canonicalQuery(v url.Values) string {
	if len(v) == 0 {
		return ""
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), v[k]...)
		sort.Strings(vals)
		for _, val := range vals {
			parts = append(parts, awsEscape(k)+"="+awsEscape(val))
		}
	}
	return strings.Join(parts, "&")
}

func awsEscape(s string) string {
	return awsURIEncode(s, true)
}

// awsURIEncode is AWS Signature Version 4 URI encoding of raw bytes.
// Unreserved bytes are only A-Z a-z 0-9 - _ . ~. Every other byte is '%'
// plus two uppercase hex digits. encodeSlash reports whether '/' is encoded.
// The input is encoded once: a literal '%' byte becomes %25, not %2525.
func awsURIEncode(s string, encodeSlash bool) string {
	const hexdigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' || (c == '/' && !encodeSlash) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexdigits[c>>4])
		b.WriteByte(hexdigits[c&0x0F])
	}
	return b.String()
}

func hmacSHA(key []byte, value string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(value))
	return m.Sum(nil)
}

func hash256(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
