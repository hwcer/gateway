package gateway

import (
	"strings"
	"testing"

	"github.com/hwcer/cosgo/values"
)

// panicHandler 业务层的 Router 实现里 panic
type panicHandler struct{ Default }

func (panicHandler) Router(string, values.Metadata) (string, string, error) {
	panic("boom from business Router")
}

// TestForwardRecoversPanic 业务钩子 panic 必须收成 error 返回。
//
// 🔴 Router / Request / Response / Serialize 都是业务层实现的，一次空指针就会顺着调用栈
// 把整条连接的处理打断——转发失败最多是这个请求报错，不该演变成掉线。
func TestForwardRecoversPanic(t *testing.T) {
	old := Setting.Handler
	defer func() { Setting.Handler = old }()
	Setting.Handler = panicHandler{}

	//推送上下文就够用：Forward 在取过 Metadata 之后立刻调 Router，正好走到 panic
	c := newSenderContext(nil, "", nil, 0, values.Metadata{})
	reply, err := Forward(c, "/game/test")
	if err == nil {
		t.Fatal("panic 必须被收成 error 返回")
	}
	if reply != nil {
		t.Fatalf("panic 路径不该返回内容,得到 %v", reply)
	}
	if !strings.Contains(err.Error(), "boom from business Router") {
		t.Fatalf("错误里应带上 panic 的内容,得到:%v", err)
	}
}
