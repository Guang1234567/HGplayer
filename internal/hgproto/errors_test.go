package hgproto

import (
	"context"
	"fmt"
	"testing"
)

// TestIsInputError 校验调用方参数错误与上游失败的区分。
//
// 上游错误文本里同样可能含「为空」「无效」（例如「官网分集列表为空」），
// 因此判定必须基于错误类型，而不是字符串匹配。
func TestIsInputError(t *testing.T) {
	var empty Client
	inputErr := func() error {
		_, err := empty.Search(context.Background(), "   ", 1)
		return err
	}()
	if inputErr == nil {
		t.Fatal("空关键词应返回错误")
	}
	if !IsInputError(inputErr) {
		t.Fatalf("空关键词错误应判定为输入错误: %v", inputErr)
	}

	upstream := []error{
		fmt.Errorf("官网分集列表为空"),
		fmt.Errorf("搜索响应为空"),
		fmt.Errorf("上游返回 HTTP 502"),
		fmt.Errorf("App 片源地址格式无效"),
		fmt.Errorf("搜索暂不可用：官网 %v；App %v", fmt.Errorf("搜索响应为空"), fmt.Errorf("上游返回空响应")),
	}
	for _, err := range upstream {
		if IsInputError(err) {
			t.Fatalf("上游错误被误判为输入错误: %v", err)
		}
	}
}
