package gateway

import (
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/context"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
)

// newKickTestPlayer 模拟真实登录+选角:Create 建认证会话(不进表),Update 落 uid 触发 rebind 入表
func newKickTestPlayer(t *testing.T, guid, uid string) *session.Data {
	t.Helper()
	if session.Options.Storage == nil {
		session.Options.Storage = session.NewMemory(16)
	}
	_, p, err := players.Create(guid, nil)
	if err != nil {
		t.Fatalf("create error:%v", err)
	}
	players.Update(p, values.Values{gwcfg.ServiceMetadataUID: uid})
	return p
}

func channelMemberCount(name, value string) int {
	room := channel.Get(name, value)
	if room == nil {
		return 0
	}
	n := 0
	room.Range(func(p *session.Data) bool {
		n++
		return true
	})
	return n
}

// player.kick. 前缀(CookiesUpdate)与 channel/kick RPC 最终都走 channel.Kick,
// 这里对核心逻辑做覆盖:按UID踢人只移除目标,发起者与其他成员不受影响
func TestChannelKick(t *testing.T) {
	name, value := "kicktest", "room1"
	leader := newKickTestPlayer(t, "leader-guid", "1001")
	member1 := newKickTestPlayer(t, "member1-guid", "1002")
	member2 := newKickTestPlayer(t, "member2-guid", "1003")
	defer func() {
		players.Delete(leader)
		players.Delete(member1)
		players.Delete(member2)
		channel.Delete(name, value)
	}()

	channel.Join(leader, name, value)
	channel.Join(member1, name, value)
	channel.Join(member2, name, value)
	if n := channelMemberCount(name, value); n != 3 {
		t.Fatalf("join后成员数=%d, want 3", n)
	}

	channel.Kick("1002", name, value)

	if n := channelMemberCount(name, value); n != 2 {
		t.Fatalf("踢人后成员数=%d, want 2", n)
	}
	if v, ok := channel.NewSetter(member1).Get(name); ok {
		t.Fatalf("被踢者会话频道记录未清理, value=%s", v)
	}
	if _, ok := channel.NewSetter(leader).Get(name); !ok {
		t.Fatalf("发起者不应被移出频道")
	}

	// 幂等:踢不在线/不在频道内的玩家无事发生
	channel.Kick("9999", name, value)
	if n := channelMemberCount(name, value); n != 2 {
		t.Fatalf("踢不存在玩家后成员数=%d, want 2", n)
	}

	// 空uid不误伤
	channel.Kick("", name, value)
	if n := channelMemberCount(name, value); n != 2 {
		t.Fatalf("空uid踢人后成员数=%d, want 2", n)
	}
}

// players 表生命周期(键=uid):选角入表、换角换绑、跨会话接管(uid级顶号)、下线摘除
func TestPlayersTableLifecycle(t *testing.T) {
	p := newKickTestPlayer(t, "guid-a", "2001")
	if players.Get("2001") != p {
		t.Fatalf("选角后未入表")
	}

	// 换角:同一会话切换角色,旧键摘除、新键建立
	players.Update(p, values.Values{gwcfg.ServiceMetadataUID: "2002"})
	if players.Get("2001") != nil {
		t.Fatalf("换角后旧键未摘")
	}
	if players.Get("2002") != p {
		t.Fatalf("换角后新键未建")
	}

	// 跨会话接管:另一会话选同一角色,老会话被 supersede(uid 清空让位)
	pb := newKickTestPlayer(t, "guid-b", "2002")
	if players.Get("2002") != pb {
		t.Fatalf("接管未生效")
	}
	if p.GetString(gwcfg.ServiceMetadataUID) != "" {
		t.Fatalf("被接管的老会话 uid 未清空")
	}

	// 被接管的老会话下线:归属校验挡住,不得误摘新会话的表项
	players.Delete(p)
	if players.Get("2002") != pb {
		t.Fatalf("老会话下线误摘了新会话的表项")
	}

	// 正常下线摘除
	players.Delete(pb)
	if players.Get("2002") != nil {
		t.Fatalf("下线后表项未摘")
	}
}

// 频道命令统一编码:key = 前缀 + ["name","value"],value 仅 Kick 携带被踢UID。
// 覆盖 CookiesUpdate 对三种命令的解析与执行
func TestCookiesUpdateChannelCommands(t *testing.T) {
	p := newKickTestPlayer(t, "guid-cookie", "6001")
	defer players.Delete(p)

	name, value := "cookietest", "r1"
	joinKey := gwcfg.ServicePlayerChannelJoin + context.ChannelNameEncode(name, value)
	leaveKey := gwcfg.ServicePlayerChannelLeave + context.ChannelNameEncode(name, value)
	kickKey := gwcfg.ServicePlayerChannelKick + context.ChannelNameEncode(name, value)

	CookiesUpdate(values.Metadata{joinKey: ""}, p, 0)
	if n := channelMemberCount(name, value); n != 1 {
		t.Fatalf("join后成员数=%d, want 1", n)
	}

	CookiesUpdate(values.Metadata{kickKey: "6001"}, p, 0)
	if n := channelMemberCount(name, value); n != 0 {
		t.Fatalf("kick后成员数=%d, want 0", n)
	}

	channel.Join(p, name, value)
	CookiesUpdate(values.Metadata{leaveKey: ""}, p, 0)
	if n := channelMemberCount(name, value); n != 0 {
		t.Fatalf("leave后成员数=%d, want 0", n)
	}
	if _, ok := channel.NewSetter(p).Get(name); ok {
		t.Fatalf("leave后会话频道记录未清理")
	}
}

// 频道一律按UID绑定:换角(uid变更)时,旧角色的频道身份必须整体清理,
// 否则换角后旧公会的广播还会推给新角色
func TestChannelSwitchUID(t *testing.T) {
	name, value := "switchtest", "room1"
	p := newKickTestPlayer(t, "guid-switch", "4001")
	defer players.Delete(p)

	channel.Join(p, name, value)
	if n := channelMemberCount(name, value); n != 1 {
		t.Fatalf("join后成员数=%d, want 1", n)
	}

	// 换角:同一会话切换到另一个角色
	players.Update(p, values.Values{gwcfg.ServiceMetadataUID: "4002"})

	if n := channelMemberCount(name, value); n != 0 {
		t.Fatalf("换角后旧角色仍在频道,成员数=%d", n)
	}
	if _, ok := channel.NewSetter(p).Get(name); ok {
		t.Fatalf("换角后会话频道记录未清理")
	}
	if players.Get("4001") != nil {
		t.Fatalf("换角后旧 uid 表项未摘")
	}

	// 新角色重新入房,以新uid为成员键
	channel.Join(p, name, value)
	if n := channelMemberCount(name, value); n != 1 {
		t.Fatalf("新角色入房后成员数=%d, want 1", n)
	}
	channel.Delete(name, value)
}

// 同账号多角色并行在线:两条独立会话互不干扰——
// 登录不踢人(顶号已是UID级),这是本次改造的核心产品语义
func TestSameAccountMultiRole(t *testing.T) {
	pa := newKickTestPlayer(t, "guid-multi", "7001")
	pb := newKickTestPlayer(t, "guid-multi", "7002")
	defer func() {
		players.Delete(pa)
		players.Delete(pb)
	}()

	if players.Get("7001") != pa || players.Get("7002") != pb {
		t.Fatalf("同账号两角色应各自在表")
	}
	players.Delete(pa)
	if players.Get("7002") != pb {
		t.Fatalf("一个角色下线不应影响另一个")
	}
}
