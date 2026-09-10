package modeldetect

import (
	"encoding/base64"
	"regexp"
	"strings"
)

// 签名内部形态族。
const (
	SigFamilyBedrock    = "bedrock"    // Bedrock 承载：内嵌公开模型 id + UUID 请求引用
	SigFamilyFirstParty = "firstparty" // 第一方 Anthropic：内嵌内部代号 + 数字请求引用
	SigFamilyVertex     = "vertex"     // Vertex：claude# 前缀
	SigFamilyUnknown    = "unknown"    // 解析失败或形态不认识
)

// SigShape thinking 签名的内部形态。
//
// 只作诊断线索：签名内部编码是 Anthropic 未公开的实现细节，对方改一次格式这里就全部
// 退化成 unknown。因此解析失败一律静默返回 unknown，绝不据此单独定罪——真正的判据
// 留给基于公开协议的 usage 守恒与 thinking 自洽。
type SigShape struct {
	// KeyLabel 签名里内嵌的标识。Bedrock 形态是公开模型 id（claude-opus-5），
	// 第一方形态是内部代号（如 claude-honey）。
	KeyLabel string
	// RequestRef 签名里内嵌的请求引用：Bedrock 形态是 UUID，第一方形态是一串数字。
	RequestRef string
	// Family 归类结果，取 SigFamily* 之一。
	Family string
}

var (
	// sigUUIDRe 标准 UUID（Bedrock 形态的请求引用）。
	sigUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// sigDigitsRe 纯数字请求引用（第一方形态）。
	sigDigitsRe = regexp.MustCompile(`^\d{6,}$`)
	// sigLabelRe 签名里可作为标识的可打印片段：字母开头，含字母数字与连字符。
	sigLabelRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{3,}$`)
)

// ParseSignatureShape 解析签名内部形态。任何异常都返回 Family=unknown，不 panic。
func ParseSignatureShape(sig string) SigShape {
	out := SigShape{Family: SigFamilyUnknown}
	sig = strings.TrimSpace(sig)
	if sig == "" {
		return out
	}
	// Vertex 在外层就带明文前缀，不必解码。
	if strings.HasPrefix(sig, "claude#") {
		out.Family = SigFamilyVertex
		return out
	}

	blob, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		// 部分渠道用无填充的 base64，补齐再试一次。
		padded := sig + strings.Repeat("=", (4-len(sig)%4)%4)
		blob, err = base64.StdEncoding.DecodeString(padded)
		if err != nil {
			return out
		}
	}

	for _, s := range protoStrings(blob, 0) {
		switch {
		case out.KeyLabel == "" && sigLabelRe.MatchString(s):
			out.KeyLabel = s
		case out.RequestRef == "" && (sigUUIDRe.MatchString(s) || sigDigitsRe.MatchString(s)):
			out.RequestRef = s
		}
	}
	out.Family = classifySigShape(out)
	return out
}

// classifySigShape 按标识与请求引用的组合归类。两个维度必须同向，否则算不认识。
//
// Bedrock 的签名内嵌的是公开模型 id 与 UUID；第一方内嵌的是内部代号与数字流水号。
// 只匹配上一半的（比如公开模型 id 配数字引用）说明形态没见过，宁可报 unknown。
func classifySigShape(s SigShape) string {
	if s.KeyLabel == "" {
		return SigFamilyUnknown
	}
	publicModel := modelIDRe.MatchString(s.KeyLabel)
	switch {
	case publicModel && sigUUIDRe.MatchString(s.RequestRef):
		return SigFamilyBedrock
	case !publicModel && (s.RequestRef == "" || sigDigitsRe.MatchString(s.RequestRef)):
		return SigFamilyFirstParty
	default:
		return SigFamilyUnknown
	}
}

// maxProtoDepth 嵌套解析深度上限。签名里需要的标识都在浅层，限深顺带防了畸形输入。
const maxProtoDepth = 3

// protoStrings 从 protobuf 字节流里收集所有可打印的字符串字段（含嵌套）。
//
// 这是个只认 varint 与 length-delimited 两种 wire type 的浅解析器：签名的 schema
// 未公开，逐字段对号入座没有意义，只把「长得像字符串的字段」捞出来交给上层归类。
// 遇到任何不合法的编码就地停止，已收集的部分照常返回。
func protoStrings(buf []byte, depth int) []string {
	var out []string
	for i := 0; i < len(buf); {
		key, n := readVarint(buf, i)
		if n <= 0 {
			return out
		}
		i = n
		field, wire := key>>3, key&7
		if field == 0 {
			return out
		}
		switch wire {
		case 0: // varint
			_, n := readVarint(buf, i)
			if n <= 0 {
				return out
			}
			i = n
		case 1: // fixed64
			if i+8 > len(buf) {
				return out
			}
			i += 8
		case 2: // length-delimited
			length, n := readVarint(buf, i)
			if n <= 0 || length > uint64(len(buf)-n) {
				return out
			}
			i = n + int(length)
			val := buf[n : n+int(length)]
			if s, ok := printableASCII(val); ok {
				out = append(out, s)
				continue
			}
			if depth < maxProtoDepth {
				out = append(out, protoStrings(val, depth+1)...)
			}
		case 5: // fixed32
			if i+4 > len(buf) {
				return out
			}
			i += 4
		default:
			return out
		}
	}
	return out
}

// readVarint 读一个 varint，返回值与新游标。编码不合法时游标返回 -1。
func readVarint(buf []byte, i int) (uint64, int) {
	var v uint64
	for shift := 0; i < len(buf); shift += 7 {
		if shift >= 64 {
			return 0, -1
		}
		b := buf[i]
		i++
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, i
		}
	}
	return 0, -1
}

// printableASCII 判断字节片是否是一整段可打印 ASCII。
func printableASCII(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return "", false
		}
	}
	return string(b), true
}
