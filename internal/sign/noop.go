package sign

// 该文件提供 Signer 的占位实现，用于 Spike 阶段让应用可运行。
//
// 真实算法实现存放在包内其他不对外暴露的文件中，由内部开发在内部引入。
// 本占位实现不做任何签名，仅保证调用链与接口契约可用，便于先行验证
// 架构与同源 HTTP 链路。生产实现接入后，本文件应被删除。

// NoopSigner 是不做任何签名的占位实现。
type NoopSigner struct{}

// Sign 实现 Signer 接口，返回空鉴权头集。
func (NoopSigner) Sign(Signature) Headers {
	return Headers{}
}

// 编译期确认 NoopSigner 满足 Signer 接口。
var _ Signer = NoopSigner{}
