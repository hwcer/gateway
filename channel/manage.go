package channel

import (
	"sync"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/logger"
)

var manage = sync.Map{}

func Get(name, value string) (r *Channel) {
	rk := Name(name, value)
	if i, ok := manage.Load(rk); ok {
		r = i.(*Channel)
	}
	return
}

func loadOrCreate(name, value string, fixed bool) (r *Channel) {
	rk := Name(name, value)
	newChannel := New(rk, fixed)
	if i, loaded := manage.LoadOrStore(rk, newChannel); loaded {
		r = i.(*Channel)
	} else {
		r = newChannel
	}
	return
}

// Join 将玩家加入指定名称和参数的频道
// 参数:
//
//	p - 玩家会话
//	name - 频道名称
//	value - 频道参数
//
// 注意: 同一名称的频道，一个角色只能加入一个；频道一律按UID绑定,无UID的会话拒绝入房
func Join(p *session.Data, name string, value string) {
	logger.Debug("channel Join name:%s value:%s", name, value)
	uid := uidOf(p)
	if uid == "" {
		logger.Error("channel Join uid empty name:%s value:%s", name, value)
		return
	}
	setter := NewSetter(p)
	if old, ok := setter.Join(name, value); ok && old != value {
		leave(p, name, old)
	}
	rk := Name(name, value)
	// room.Join 返回 false 表示命中了正在销毁(released)的旧实例:
	// 清掉该实例后重试,避免"加入"与"空房销毁"并发时玩家被静默丢弃
	for i := 0; i < 8; i++ {
		room := loadOrCreate(name, value, false)
		if room.Join(uid, p) {
			return
		}
		manage.CompareAndDelete(rk, room)
	}
	logger.Error("channel Join failed after retries name:%s value:%s", name, value)
}

// Leave 将玩家从指定频道移除
// 参数:
//
//	p - 玩家会话
//	name - 频道名称
//	value - 频道参数
func Leave(p *session.Data, name string, value string) {
	setter := NewSetter(p)
	setter.Leave(name, value)
	leave(p, name, value)
}

func leave(p *session.Data, name, value string) {
	logger.Debug("channel Leave name:%s value:%s", name, value)
	if room := Get(name, value); room != nil {
		room.Leave(uidOf(p))
	}
}

// Kick 将指定UID的玩家踢出频道
// 参数:
//
//	uid - 被踢玩家的角色ID
//	name - 频道名称
//	value - 频道参数
//
// 成员表按UID键,直接在频道内定位;不在频道内或频道不存在均视为成功(幂等)
func Kick(uid, name, value string) {
	if uid == "" {
		logger.Debug("channel Kick uid is empty name:%s value:%s", name, value)
		return
	}
	room := Get(name, value)
	if room == nil {
		logger.Debug("channel Kick room not found name:%s value:%s", name, value)
		return
	}
	p := room.Member(uid)
	if p == nil {
		logger.Debug("channel Kick player not in channel uid:%s name:%s value:%s", uid, name, value)
		return
	}
	logger.Debug("channel Kick uid:%s name:%s value:%s", uid, name, value)
	NewSetter(p).Leave(name, value)
	room.Leave(uid)
}

// SwitchUID 会话换角(uid变更)时清理旧角色的频道身份:
// 旧记录一律按旧UID从房间移除,新角色从零开始加入
func SwitchUID(p *session.Data, oldUID string) {
	rs := NewSetter(p).Release()
	for _, r := range rs {
		logger.Debug("channel SwitchUID leave uid:%s name:%s value:%s", oldUID, r.k, r.v)
		if room := Get(r.k, r.v); room != nil {
			room.Leave(oldUID)
		}
	}
}

func Range(name, value string, f func(*session.Data) bool) {
	room := Get(name, value)
	if room == nil {
		return
	}
	room.Range(f)
}

// Release 用户掉线,销毁时 清理所在房间信息
func Release(p *session.Data) {
	setter := NewSetter(p)
	rs := setter.Release()
	for _, r := range rs {
		leave(p, r.k, r.v)
	}
}

// Delete 销毁房间
func Delete(name, value string) {
	rk := Name(name, value)
	i, loaded := manage.LoadAndDelete(rk)
	if !loaded {
		return
	}
	room := i.(*Channel)
	room.Release()
}
