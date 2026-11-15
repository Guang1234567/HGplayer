package hgproto

import (
	"errors"
	"fmt"
)

// inputError 表示调用方参数错误（与上游失败区分）。
//
// 服务层据此返回 400，上游失败一律 502。区分不依赖错误文本：
// 上游错误的文本里同样可能出现「为空」「无效」等字样（例如
// 「搜索响应为空」「官网分集列表为空」），字符串匹配会误判。
type inputError struct {
	msg string
}

// Error 实现 error 接口。
func (e inputError) Error() string { return e.msg }

// inputErrf 构造调用方参数错误。
func inputErrf(format string, args ...any) error {
	return inputError{msg: fmt.Sprintf(format, args...)}
}

// IsInputError 判断错误是否由调用方参数引起。
func IsInputError(err error) bool {
	var target inputError
	return errors.As(err, &target)
}
