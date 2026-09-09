package gateway

import (
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"
)

func newKickTestPlayer(uuid, uid string) *session.Data {
	return session.NewData(uuid, map[string]any{gwcfg.ServiceMetadataUID: uid})
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

// CookiesUpdate 里的 player.kick. 前缀最终走 channel.Kick,
// 这里直接对核心逻辑做覆盖:踢人只移除目标,发起者与其他成员不受影响
func TestChannelKick(t *testing.T) {
	name, value := "kicktest", "room1"
	leader := newKickTestPlayer("leader-guid", "1001")
	member1 := newKickTestPlayer("member1-guid", "1002")
	member2 := newKickTestPlayer("member2-guid", "1003")

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

	// 幂等:踢不在频道内的玩家无事发生
	channel.Kick("9999", name, value)
	if n := channelMemberCount(name, value); n != 2 {
		t.Fatalf("踢不存在玩家后成员数=%d, want 2", n)
	}

	// 空uid不误伤(不会踢中没设置uid的成员)
	channel.Kick("", name, value)
	if n := channelMemberCount(name, value); n != 2 {
		t.Fatalf("空uid踢人后成员数=%d, want 2", n)
	}

	channel.Delete(name, value)
}
