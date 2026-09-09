package gateway

import (
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
)

func newKickTestPlayer(t *testing.T, guid, uid string) *session.Data {
	t.Helper()
	if session.Options.Storage == nil {
		session.Options.Storage = session.NewMemory(16)
	}
	// 模拟真实登录:Login 会写 players 表并维护 UID->GUID 映射
	_, p, err := players.Login(guid, values.Values{gwcfg.ServiceMetadataUID: uid})
	if err != nil {
		t.Fatalf("login error:%v", err)
	}
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

// UID->GUID 映射生命周期:换角解绑旧UID,下线解绑,UID转移后不误删新账号的映射
func TestPlayersUIDMapping(t *testing.T) {
	p := newKickTestPlayer(t, "guid-a", "2001")
	defer players.Delete(p)

	if g := players.GUID("2001"); g != "guid-a" {
		t.Fatalf("映射未建立, GUID(2001)=%s", g)
	}

	// 换角:同账号重新登录切换角色,旧UID必须解绑,否则踢旧角色会误伤新会话
	if _, p2, err := players.Login("guid-a", values.Values{gwcfg.ServiceMetadataUID: "2002"}); err != nil {
		t.Fatalf("relogin error:%v", err)
	} else if p2 != p {
		t.Fatalf("同GUID重登应复用会话")
	}
	if g := players.GUID("2001"); g != "" {
		t.Fatalf("换角后旧UID未解绑, GUID(2001)=%s", g)
	}
	if g := players.GUID("2002"); g != "guid-a" {
		t.Fatalf("换角后新UID未绑定, GUID(2002)=%s", g)
	}

	// UID转移到别的账号:原会话下线不能删掉新账号的映射
	_ = newKickTestPlayer(t, "guid-b", "2002")
	pb := players.Get("guid-b")
	defer players.Delete(pb)
	players.Delete(p)
	if g := players.GUID("2002"); g != "guid-b" {
		t.Fatalf("UID转移后映射被误删, GUID(2002)=%s", g)
	}

	// 下线解绑
	players.Delete(pb)
	if g := players.GUID("2002"); g != "" {
		t.Fatalf("下线后映射未解绑, GUID(2002)=%s", g)
	}
}

// 频道一律按UID绑定:换角(会话uid变更)时,旧角色的频道身份必须整体清理,
// 否则换角后旧公会的广播还会推给新角色
func TestChannelSwitchUID(t *testing.T) {
	name, value := "switchtest", "room1"
	p := newKickTestPlayer(t, "guid-switch", "4001")
	defer players.Delete(p)

	channel.Join(p, name, value)
	if n := channelMemberCount(name, value); n != 1 {
		t.Fatalf("join后成员数=%d, want 1", n)
	}

	// 换角重登:同一账号切换到另一个角色
	if _, p2, err := players.Login("guid-switch", values.Values{gwcfg.ServiceMetadataUID: "4002"}); err != nil {
		t.Fatalf("relogin error:%v", err)
	} else if p2 != p {
		t.Fatalf("同GUID重登应复用会话")
	}

	if n := channelMemberCount(name, value); n != 0 {
		t.Fatalf("换角后旧角色仍在频道,成员数=%d", n)
	}
	if _, ok := channel.NewSetter(p).Get(name); ok {
		t.Fatalf("换角后会话频道记录未清理")
	}

	// 新角色重新入房,以新UID为成员键
	channel.Join(p, name, value)
	if n := channelMemberCount(name, value); n != 1 {
		t.Fatalf("新角色入房后成员数=%d, want 1", n)
	}
	channel.Delete(name, value)
}
