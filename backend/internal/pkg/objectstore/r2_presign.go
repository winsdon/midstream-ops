package objectstore

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	presignDefaultTTL = 5 * time.Minute
	presignMaxTTL     = 15 * time.Minute
)

// PresignPut 签发浏览器直传的预签名 PUT。
//
// 查询串签名（不走 Authorization 头）：浏览器跨域 PUT 只能带有限几个头，
// 把签名放进 URL 后，前端只需再带上签名时锁定的 Content-Type。
func (r *R2) PresignPut(key, contentType string, expires time.Duration) (string, string, error) {
	key = strings.TrimPrefix(strings.TrimSpace(key), "/")
	if key == "" {
		return "", "", fmt.Errorf("对象键不能为空")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if expires <= 0 {
		expires = presignDefaultTTL
	}
	if expires > presignMaxTTL {
		expires = presignMaxTTL
	}

	t := r.now().UTC()
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")
	scope := strings.Join([]string{dateStamp, r2Region, r2Service, "aws4_request"}, "/")
	credential := r.cfg.AccessKeyID + "/" + scope
	signedHeaders := "content-type;host"

	query := map[string]string{
		"X-Amz-Algorithm":      "AWS4-HMAC-SHA256",
		"X-Amz-Content-Sha256": unsignedPayload,
		"X-Amz-Credential":     credential,
		"X-Amz-Date":           amzDate,
		"X-Amz-Expires":        strconv.FormatInt(int64(expires.Seconds()), 10),
		"X-Amz-SignedHeaders":  signedHeaders,
	}

	canonicalQuery := encodeAWSQuery(query)
	canonicalHeaders := "content-type:" + contentType + "\n" + "host:" + r.host + "\n"
	objectPath := "/" + r.cfg.Bucket + "/" + key

	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		objectPath,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		unsignedPayload,
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	sig := hex.EncodeToString(hmacSHA256(signingKey(r.cfg.SecretAccessKey, dateStamp), []byte(stringToSign)))
	upload := r.endpoint + objectPath + "?" + canonicalQuery + "&X-Amz-Signature=" + sig
	public := r.cfg.PublicBaseURL + "/" + key
	return upload, public, nil
}

// encodeAWSQuery 按名称排序并做 AWS 风格转义（空格是 %20 不是 +）。
func encodeAWSQuery(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, awsQueryEscape(k)+"="+awsQueryEscape(values[k]))
	}
	return strings.Join(parts, "&")
}

func awsQueryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
