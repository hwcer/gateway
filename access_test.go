package gateway

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hwcer/gateway/gwcfg"
)

// TestAccessSelectCarriesUid Select 级必须下发 uid。
//
// 🔴 这条钉的是 Select 与 Player **共用同一个 access 实现**这件事。
// 三档的差别只在业务服那侧(Select 不进玩家协程),在网关这侧它们同样要求已选角、
// 同样把 uid 写进 metadata。
//
// 把 Select 改注册到 Access.OAuth 会怎样:不报错、不崩,只是 uid 静默消失
// (OAuth 只下发 GUID)。业务侧 c.Uid() 恒为空串,凡是靠 uid 认人的接口全部
// 变成"查谁都查不到" —— 而这类接口正是 Select 级的典型用户(读共享数据、
// 但要知道请求者是谁,如公会信息 / 列表)。这种错只能靠真机点一下才发现。
func TestAccessSelectCarriesUid(t *testing.T) {
	sel, ok := Access.dict[gwcfg.OAuthTypeSelect]
	if !ok {
		t.Fatal("OAuthTypeSelect 没有注册 access 实现")
	}
	ply, ok := Access.dict[gwcfg.OAuthTypePlayer]
	if !ok {
		t.Fatal("OAuthTypePlayer 没有注册 access 实现")
	}
	if reflect.ValueOf(sel).Pointer() != reflect.ValueOf(ply).Pointer() {
		t.Fatal("Select 与 Player 用了不同的 access 实现:" +
			"Select 必须复用 Access.Player(要求已选角并下发 uid)。" +
			"若确要拆开,请确认新实现同样写入 ServiceMetadataUID,否则 c.Uid() 会恒为空串")
	}
	oa := Access.dict[gwcfg.OAuthTypeOAuth]
	if reflect.ValueOf(sel).Pointer() == reflect.ValueOf(oa).Pointer() {
		t.Fatal("Select 被注册成了 Access.OAuth:那一档只下发 GUID、不下发 uid")
	}
}

// TestAccessAllLevelsRegistered 每个 OAuthType 都要有实现。
//
// 漏一个的表现是 Verify 返回 "unknown authorization type: N" —— 该档位的
// 所有接口整体不可用,而不是降级。
func TestAccessAllLevelsRegistered(t *testing.T) {
	for _, l := range []gwcfg.OAuthType{
		gwcfg.OAuthTypeNone, gwcfg.OAuthTypeOAuth,
		gwcfg.OAuthTypeSelect, gwcfg.OAuthTypePlayer,
	} {
		if _, ok := Access.dict[l]; !ok {
			t.Fatalf("OAuthType %v 没有注册 access 实现", l)
		}
	}
}

// TestAccessPlayerSendsIdentity Select / Player 档必须下发 GUID 与 UID。
//
// 🔴 缺 GUID 的后果是**推送静默丢弃**：网关的 send() 按 GUID 定位会话
// （players.Get(guid)），拿不到就只打一行 Debug 日志。游戏服看不出来 —— 它的
// c.Player 非空，推送走 player.Send 自带 GUID；而没有玩家容器的服务（社交服等）
// 只能走 Context.Send 的另一条分支，表现是「接口调通了、客户端什么也没收到」。
//
// 用源码文本断言而不是跑一次 Verify：构造 Request / session.Data 要拖进网络与存储，
// 而这条要钉的就是「那几行别被删掉」。
func TestAccessPlayerSendsIdentity(t *testing.T) {
	src, err := os.ReadFile("access.go")
	if err != nil {
		t.Fatalf("读取 access.go 失败:%v", err)
	}
	body := string(src)
	i := strings.Index(body, "func (this *access) Player(")
	if i < 0 {
		t.Fatal("找不到 access.Player")
	}
	fn := body[i:]
	if j := strings.Index(fn[1:], "\nfunc "); j > 0 {
		fn = fn[:j]
	}
	for _, want := range []string{
		"req[gwcfg.ServiceMetadataGUID]",
		"req[gwcfg.ServiceMetadataUID]",
		"req[gwcfg.ServiceMetadataSocketId]",
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("access.Player 不再下发 %v —— 没有玩家容器的服务(社交服等)"+
				"会推送不出去，且只打日志不报错", want)
		}
	}
}
