package sign

import "crypto/md5"

// md5Digest 返回输入的 MD5 摘要（16 字节）。
func md5Digest(data []byte) []byte {
	h := md5.Sum(data)
	return h[:]
}

// md5HexUpper 返回大写十六进制 MD5。
func md5HexUpper(data []byte) string {
	return toUpper(hexEncode(md5Digest(data)))
}

// —— 十六进制编解码 ——

const hexDigits = "0123456789abcdef"

// hexEncode 编码为小写十六进制。
func hexEncode(b []byte) string {
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexDigits[v>>4]
		out[i*2+1] = hexDigits[v&0xF]
	}
	return string(out)
}

// hexDecode 解析十六进制字符串。
func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errHexOdd
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err := hexVal(s[i*2])
		if err != nil {
			return nil, err
		}
		lo, err := hexVal(s[i*2+1])
		if err != nil {
			return nil, err
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexVal(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, errHexChar
}

type hexErr string

func (e hexErr) Error() string { return string(e) }

const (
	errHexOdd  hexErr = "十六进制长度为奇数"
	errHexChar hexErr = "非法十六进制字符"
)
