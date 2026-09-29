package modeldetect

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 平台族。消息 id、模型回显、篡改报错三个维度各自归一到其中之一，供跨项审计比对。
//
// 签名内部只认得出 Vertex（外层明文前缀 claude#）。早先按「公开模型 id + UUID = Bedrock、
// 内部代号 + 数字 = 第一方」归类，后来证实那是签名 schema 版本的差别而不是平台差别：
// 2026-09 实测第一方 CC Max 渠道的 opus-5 签名同样内嵌 claude-opus-5 + UUID，结构与
// Bedrock 样本一致。据此判平台会给每条第一方渠道记一条假的「平台形态矛盾」。
const (
	SigFamilyBedrock    = "bedrock"
	SigFamilyFirstParty = "firstparty"
	SigFamilyVertex     = "vertex"
	SigFamilyUnknown    = "unknown"
)

// SigShape thinking 签名里能读出的字段。
//
// 签名是 base64 + protobuf，schema 未公开，字段号全部来自 2026-09 的实测样本（schema v17 / v18）：
// f2.f1.f1 schema 版本、f2.f1.f6 模型、f2.f1.f11 账号标识（同一账号跨请求不变）、
// f2.f1.f15 用户档案（只有 OAuth 订阅号带 uprof_）、f2.f1.f21 签发时间（Unix 秒）。
// 其他 schema 版本只读版本号与模型名。对方改一次格式这里就读不出东西，所以解析失败
// 一律静默返回零值，由跨项审计记一条「签名来源不可读」的诊断。
type SigShape struct {
	Version    int
	Model      string
	AccountRef string
	Profile    string
	IssuedAt   time.Time
	// Family 只区分 vertex 与 unknown，原因见平台族常量的注释。
	Family string
}

// 签名 protobuf 里的字段路径（字段号用点连接）。
const (
	sigPathVersion  = "2.1.1"
	sigPathModel    = "2.1.6"
	sigPathAccount  = "2.1.11"
	sigPathProfile  = "2.1.15"
	sigPathIssuedAt = "2.1.21"
)

// sigSchemaVerified 字段含义实测过的 schema 版本。其他版本只读版本号与模型名：
// v15 样本里就没有账号与时间字段，字段号的含义会随版本变化，硬读只会得到错位的值。
// v18 于 2026-09-22 出现，两份报告里 f11 都是合法的 v4 UUID、同一账号跨请求不变，
// f21 与本轮开始只差几秒，与 v17 同义。
var sigSchemaVerified = map[int]bool{17: true, 18: true}

// 签发时间的合理区间，挡掉恰好落在这个字段号上的无关整数。
var (
	sigTimeFloor = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	sigTimeCeil  = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
)

var (
	// sigUUIDShapeRe 8-4-4-4-12 的十六进制 UUID 外形（不管版本位）。
	sigUUIDShapeRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// sigUUIDValidRe 合法 RFC 9562 UUID：版本位 1-8、变体位 8-b。
	// 实测官方签名里的账号标识全是 v4；外形像 UUID 而版本位非法的是凭空编的。
	sigUUIDValidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// ParseSignatureShape 解析签名里的字段。任何异常都返回零值（Family=unknown），不 panic。
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

	fields := map[string]protoField{}
	protoFields(blob, "", 0, fields)
	out.Version = int(fields[sigPathVersion].num)
	out.Model = fields[sigPathModel].str
	if !sigSchemaVerified[out.Version] {
		return out
	}
	out.AccountRef = fields[sigPathAccount].str
	if p := fields[sigPathProfile].str; strings.HasPrefix(p, "uprof_") {
		out.Profile = p
	}
	if ts := int64(fields[sigPathIssuedAt].num); ts > sigTimeFloor && ts < sigTimeCeil {
		out.IssuedAt = time.Unix(ts, 0)
	}
	return out
}

// UUIDForged 账号标识外形像 UUID、版本位却不合法。真签名器不会产出这种值。
func (s SigShape) UUIDForged() bool {
	return sigUUIDShapeRe.MatchString(s.AccountRef) && !sigUUIDValidRe.MatchString(s.AccountRef)
}

// exchangeSignatures 一次成功响应里的全部 thinking 签名（含 redacted_thinking 的加密数据），同一响应内去重。
//
// 签名内含逐次随机的 nonce 与数据密钥，两次独立生成不可能相同，所以它是判「回放缓存响应」
// 最硬的去重键：网关可以每次重写消息 id，却造不出新签名。流式响应从 signature_delta 里拼；
// 进程内没有解析结果时（重试恢复的旧结果）回退到原文，原文被截断解析不出就跳过。
func exchangeSignatures(ex *Exchange) []string {
	if ex == nil || !ex.OK() {
		return nil
	}
	body, events := ex.JSON, ex.Events
	if body == nil && len(events) == 0 {
		if raw := strings.TrimSpace(ex.Raw); strings.HasPrefix(raw, "{") {
			body = parseJSONObject(raw)
		} else {
			events = parseSSE(raw)
		}
	}
	var sigs []string
	if body != nil {
		sigs = blockSignatures(contentBlocks(body))
	} else {
		sigs = streamSignatures(events)
	}
	return uniqueStrings(sigs)
}

// uniqueStrings 按首次出现的顺序去重。
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// blockSignatures 非流式响应 content 里的签名。
func blockSignatures(blocks []any) []string {
	var out []string
	for _, raw := range blocks {
		blk := mapOf(raw)
		sig := ""
		switch str(blk["type"]) {
		case "thinking":
			sig = str(blk["signature"])
		case "redacted_thinking":
			sig = str(blk["data"])
		}
		if sig != "" {
			out = append(out, sig)
		}
	}
	return out
}

// streamSignatures 流式响应里按内容块取出的签名。官方在 content_block_start 里给空签名、
// 由 signature_delta 下发整段；有代理两处都带，这时以 delta 为准，不拼接。
func streamSignatures(events []SSEEvent) []string {
	starts := map[int]string{}
	deltas := map[int]*strings.Builder{}
	var order []int
	seen := func(idx int) {
		if _, ok := starts[idx]; ok {
			return
		}
		if _, ok := deltas[idx]; ok {
			return
		}
		order = append(order, idx)
	}
	for _, ev := range events {
		idx := intOf(ev.Data["index"])
		switch ev.Type {
		case "content_block_start":
			blk := mapOf(ev.Data["content_block"])
			sig := ""
			switch str(blk["type"]) {
			case "thinking":
				sig = str(blk["signature"])
			case "redacted_thinking":
				sig = str(blk["data"])
			}
			if sig != "" {
				seen(idx)
				starts[idx] = sig
			}
		case "content_block_delta":
			if delta := mapOf(ev.Data["delta"]); str(delta["type"]) == "signature_delta" && str(delta["signature"]) != "" {
				seen(idx)
				if deltas[idx] == nil {
					deltas[idx] = &strings.Builder{}
				}
				deltas[idx].WriteString(str(delta["signature"]))
			}
		}
	}
	out := make([]string, 0, len(order))
	for _, idx := range order {
		if b := deltas[idx]; b != nil {
			out = append(out, b.String())
			continue
		}
		out = append(out, starts[idx])
	}
	return out
}

// tamperSig 改签名里的一个字节，同时保持 protobuf 结构有效。
//
// 改首字符会把第一个字段号改成 0，结构当场失效——不持有密钥的代理做一次结构检查就能认出它、
// 单独剥掉，于是本该暴露的「不校验签名」变成了「剥离 thinking」。这里改最长那段不透明字节
// （加密载荷）正中间的一位：长度、字段号、版本号都不变，只有持有密钥的一方才能发现。
// 不是 base64 / protobuf 的签名（测试桩或未知格式）退回改正中间的一个字符。
func tamperSig(sig string) string {
	if blob, err := base64.StdEncoding.DecodeString(sig); err == nil {
		if start, end := largestOpaque(blob, 0); end > start {
			cp := append([]byte(nil), blob...)
			cp[(start+end)/2] ^= 0x01
			return base64.StdEncoding.EncodeToString(cp)
		}
	}
	if sig == "" {
		return sig
	}
	i := len(sig) / 2
	repl := byte('A')
	if sig[i] == 'A' {
		repl = 'B'
	}
	return sig[:i] + string(repl) + sig[i+1:]
}

// largestOpaque 最长的一段不透明字节（解析不成嵌套消息的 length-delimited 值）在 buf 里的区间。
// 解析得成消息的值往里找，不把它整段当成可改的字节——那样可能改到内层的字段号或长度。
func largestOpaque(buf []byte, depth int) (start, end int) {
	for i := 0; i < len(buf); {
		key, n := readVarint(buf, i)
		if n <= 0 {
			return start, end
		}
		i = n
		field, wire := key>>3, key&7
		if field == 0 {
			return start, end
		}
		switch wire {
		case 0:
			if _, n = readVarint(buf, i); n <= 0 {
				return start, end
			}
			i = n
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			length, n := readVarint(buf, i)
			if n <= 0 || length > uint64(len(buf)-n) {
				return start, end
			}
			vs, ve := n, n+int(length)
			i = ve
			s, e := vs, ve
			if depth < maxProtoDepth && isProtoMessage(buf[vs:ve]) {
				cs, ce := largestOpaque(buf[vs:ve], depth+1)
				s, e = vs+cs, vs+ce
			}
			if e-s > end-start {
				start, end = s, e
			}
		default:
			return start, end
		}
		if i > len(buf) {
			return start, end
		}
	}
	return start, end
}

// isProtoMessage 整段字节能不能完整解析成字段号非零的 protobuf 消息。
func isProtoMessage(buf []byte) bool {
	if len(buf) == 0 {
		return false
	}
	for i := 0; i < len(buf); {
		key, n := readVarint(buf, i)
		if n <= 0 || key>>3 == 0 {
			return false
		}
		i = n
		switch key & 7 {
		case 0:
			if _, n = readVarint(buf, i); n <= 0 {
				return false
			}
			i = n
		case 1:
			if i+8 > len(buf) {
				return false
			}
			i += 8
		case 5:
			if i+4 > len(buf) {
				return false
			}
			i += 4
		case 2:
			length, n := readVarint(buf, i)
			if n <= 0 || length > uint64(len(buf)-n) {
				return false
			}
			i = n + int(length)
		default:
			return false
		}
	}
	return true
}

// protoField protobuf 里的一个标量字段：varint 或可打印字符串。
type protoField struct {
	num uint64
	str string
}

// maxProtoDepth 嵌套解析深度上限。要读的字段都在第三层以内，限深顺带防了畸形输入。
const maxProtoDepth = 3

// protoFields 把 protobuf 字节流里的标量字段按路径收进 out（同一路径只留第一次出现）。
//
// 这是个只认 varint 与 length-delimited 的浅解析器：schema 未公开，只把「能读成标量
// 的字段」按路径摊平，由调用方按实测字段号去取。遇到不合法的编码就地停止，已收集的照常保留。
func protoFields(buf []byte, prefix string, depth int, out map[string]protoField) {
	for i := 0; i < len(buf); {
		key, n := readVarint(buf, i)
		if n <= 0 {
			return
		}
		i = n
		field, wire := key>>3, key&7
		if field == 0 {
			return
		}
		path := strconv.FormatUint(field, 10)
		if prefix != "" {
			path = prefix + "." + path
		}
		switch wire {
		case 0: // varint
			v, n := readVarint(buf, i)
			if n <= 0 {
				return
			}
			i = n
			if _, seen := out[path]; !seen {
				out[path] = protoField{num: v}
			}
		case 1: // fixed64
			if i+8 > len(buf) {
				return
			}
			i += 8
		case 2: // length-delimited
			length, n := readVarint(buf, i)
			if n <= 0 || length > uint64(len(buf)-n) {
				return
			}
			i = n + int(length)
			val := buf[n:i]
			if s, ok := printableASCII(val); ok {
				if _, seen := out[path]; !seen {
					out[path] = protoField{str: s}
				}
				continue
			}
			if depth < maxProtoDepth {
				protoFields(val, path, depth+1, out)
			}
		case 5: // fixed32
			if i+4 > len(buf) {
				return
			}
			i += 4
		default:
			return
		}
	}
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
