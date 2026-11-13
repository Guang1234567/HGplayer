// Package sign 是内部加密算法的隔离边界。
//
// 该包的实现（签名/派生/校验等内部算法）仅限内部可见，不对外暴露，
// 也不进入任何外部协作上下文。业务层只通过本文件定义的接口依赖签名能力，
// 从而保证算法实现可被内部替换、升级，而不影响其余模块。
//
// 本文件中不含任何真实算法实现；算法实现存放在包内不对外暴露的文件中，
// 由内部开发通过同包内文件引入，外部无法导入 internal/ 下的包。
package sign

// Signature 描述一次需要签名的上游请求。
//
// 业务层在发起上游请求前构造该结构并交给 Signer 处理，
// 由 Signer 注入最终的鉴权头。
type Signature struct {
	// Method 是 HTTP 方法（GET/POST）。
	Method string
	// URL 是目标 URL（含 path 与 query），签名与请求最终发往的地址一致。
	URL string
	// Query 是参与签名的原始查询串（不含 ts/_rticket 等时间戳参数）。
	Query string
	// Body 是参与签名的请求体，GET 请求为空。
	Body []byte
	// DeviceID / IID 是当前匿名设备身份。
	DeviceID string
	IID      string
	// AccountToken 是可选的账号会话凭证；未登录时为空。
	AccountToken string
}

// Headers 是签名完成后注入的上游鉴权头集合。
type Headers map[string]string

// Signer 是签名能力的统一接口。
//
// 所有上游请求在发出前必须经由此接口，以确保鉴权头一致性。
type Signer interface {
	// Sign 对请求签名，返回需要注入上游请求的鉴权头。
	Sign(sig Signature) Headers
}

// SignerFunc 把函数适配为 Signer 接口，便于测试与函数式注入。
type SignerFunc func(Signature) Headers

// Sign 实现 Signer 接口。
func (f SignerFunc) Sign(sig Signature) Headers { return f(sig) }

// 编译期确认 SignerFunc 满足 Signer 接口。
var _ Signer = SignerFunc(nil)

// KeyDeriver 是媒体内容密钥派生的接口。
//
// 密钥派生属于内部加密算法，接口在此暴露，实现存放在包内
// 不对外暴露的文件中，由内部开发接入。业务层与 cenc 包只依赖接口。
type KeyDeriver interface {
	// DeriveContentKey 从加密信息（如 spade 参数）派生 16 字节内容密钥。
	DeriveContentKey(encryptInfo []byte) ([]byte, error)
}
