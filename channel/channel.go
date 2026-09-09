package channel

import (
	"sync"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/logger"
)

// New 创建一个新的频道实例
// 参数:
//
//	name - 频道ID
//	fixed - 是否为固定频道（固定频道不会自动删除）
//
// 返回值:
//
//	新创建的频道实例
func New(name string, fixed bool) *Channel {
	return &Channel{id: name, fixed: fixed, ps: map[string]*session.Data{}}
}

type Channel struct {
	id string
	// ps 成员表:UID(角色ID) -> 会话。频道一律按UID绑定——一个账号可能有多个角色,
	// 分属不同频道;同一时间一个会话只代表一个在线角色。
	ps       map[string]*session.Data
	fixed    bool //固定频道不会自动删除
	locker   sync.RWMutex
	released bool //已经删除 无法进入
}

func (this *Channel) Id() string {
	return this.id
}

// Join 会话入房,uid 为成员键(由 manage.Join 保证非空)
func (this *Channel) Join(uid string, d *session.Data) bool {
	// 快速路径检查：使用读锁检查玩家是否已经在频道中
	this.locker.RLock()
	exists := this.ps[uid] != nil
	released := this.released
	this.locker.RUnlock()

	if exists {
		return true
	}
	if released {
		return false
	}

	this.locker.Lock()
	defer this.locker.Unlock()
	// 双重检查，避免加锁期间其他协程已经添加了该玩家或频道被释放
	if this.released {
		return false
	}
	if _, exists := this.ps[uid]; exists {
		return true
	}
	this.ps[uid] = d
	return true
}

// Member 按UID取频道内的成员会话,不在频道内返回nil
func (this *Channel) Member(uid string) *session.Data {
	this.locker.RLock()
	defer this.locker.RUnlock()
	return this.ps[uid]
}

// Leave 按UID移除成员
func (this *Channel) Leave(uid string) bool {
	this.locker.Lock()
	defer this.locker.Unlock()

	// 检查玩家是否在频道中
	if _, exists := this.ps[uid]; !exists {
		return false
	}

	delete(this.ps, uid)
	if !this.fixed && len(this.ps) == 0 {
		this.released = true
		manage.Delete(this.id)
		logger.Debug("人数为空，房间销毁:%s", this.id)
	}
	return true
}

func (this *Channel) Release() {
	this.locker.Lock()
	defer this.locker.Unlock()
	this.released = true
	this.removeAllPlayer()
	//manage.Delete(this.id)
}

// removeAllPlayer 房间销毁时，清理所有房间内的成员
// 注意：该方法只能在已获取写锁的情况下调用
func (this *Channel) removeAllPlayer() {
	k, v := Split(this.id)
	for _, d := range this.ps {
		setter := NewSetter(d)
		setter.Leave(k, v)
	}
}

func (this *Channel) Range(f func(*session.Data) bool) {
	// 先获取所有玩家的副本，然后在锁外执行回调
	var players []*session.Data
	this.locker.RLock()
	players = make([]*session.Data, 0, len(this.ps)) // 预分配内存
	for _, p := range this.ps {
		players = append(players, p)
	}
	this.locker.RUnlock()

	// 在锁外遍历并调用回调
	for _, p := range players {
		if !f(p) {
			return
		}
	}
}

func (this *Channel) Broadcast(path string, data []byte) {
	this.Range(func(p *session.Data) bool {
		SendMessage(p, path, data)
		return true
	})
}

// uidOf 读取会话当前的角色ID
func uidOf(d *session.Data) string {
	return d.GetString(gwcfg.ServiceMetadataUID)
}
