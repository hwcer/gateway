package players

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"
)

// players 会话表:**键 = UID(角色ID),只收已选角的会话**。
//
// 认证(登录)阶段的会话不进表——它们住在 socket.data(TCP/WSS)或 cookie/存储(HTTP),
// 没有任何"按账号查会话"的需求:**同账号多角色并行在线是合法状态**。
// 顶号因此是 UID 级而不是账号级:登录不踢任何人,角色占用在选角回包落地时处理(见 rebind)。
var players = sync.Map{}

// newID 会话身份:id 用"guid/随机段"的不透明值,与账号解耦。
// guid 前缀仅为日志可读,唯一性由随机段保证——同账号多角色并行时各会话的
// 存储(Redis 键)/token 互不干扰,新登录也不会作废旧会话的 token。
func newID(guid string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		//crypto/rand 失败基本等于系统异常,退化为纳秒时间戳仍保唯一
		return fmt.Sprintf("%s/%x/%d", guid, b, time.Now().UnixNano())
	}
	return guid + "/" + hex.EncodeToString(b)
}

// Create 认证登录:新建会话并写入存储,返回 token。**不进会话表**。
//
// guid 降级为会话 values 里的普通字段(ServiceMetadataGUID),业务侧经转发 metadata
// 照常拿到。角色要等选角回包(CookiesUpdate→Update→rebind)落地才进表。
// 与旧 Login 的区别:不再按 guid 复用会话、不再 Refresh 旧 TOKEN——
// 同账号的每一次登录都是一个独立会话,互相不踢。
func Create(guid string, value values.Values) (token string, data *session.Data, err error) {
	data = session.NewData(newID(guid), value)
	data.Set(gwcfg.ServiceMetadataGUID, guid)
	ss := session.New(data)
	token, err = ss.New(data)
	return
}

// Update 更新会话数据;uid 发生变化(首次选角/换角)时同步维护会话表
func Update(p *session.Data, vs values.Values) {
	if p == nil || len(vs) == 0 {
		return
	}
	old := p.GetString(gwcfg.ServiceMetadataUID)
	p.Update(vs)
	rebind(p, old)
}

// rebind uid 变化(首次选角/换角)时维护会话表。
//
// 表键=uid,一个角色同一时间只允许一个在线会话:新 uid 已被别的会话占着时,
// 本会话**接管**(supersede 老会话)。这就是 UID 级顶号,触发点是选角回包落地——
// 网关不解析协议体,选角之前无从知道目标角色,所以登录阶段不可能做占用判断。
//
// 推送/响应的 metadata 反复携带同一 uid 是常态(uid==oldUID 的快路径必须零成本)。
func rebind(p *session.Data, oldUID string) {
	uid := p.GetString(gwcfg.ServiceMetadataUID)
	if uid == oldUID {
		return
	}
	//接管:目标角色已在别的会话上在线(同账号另一台设备,或断线期间被顶)
	if uid != "" {
		if v, ok := players.Load(uid); ok {
			if os, _ := v.(*session.Data); os != nil && os != p {
				supersede(os, uid, p)
			}
		}
	}
	if oldUID != "" {
		//归属校验后才摘旧键:被接管过的会话 uid 已清空走不到这里,
		//同一 uid 短暂归属过别的会话时也不能误摘别人的表项
		if v, ok := players.Load(oldUID); ok && v == p {
			players.Delete(oldUID)
		}
		//旧角色的频道身份一并清理,否则换角后旧频道的广播还会推给新角色
		channel.SwitchUID(p, oldUID)
	}
	if uid != "" {
		players.Store(uid, p)
	}
}

// supersede 新会话接管 uid 时对老会话的处置:
//
// ① 连接进"只收不发"存活期(在途回包照常送达、新请求被拒、到期断开),
//    ip 是新端的地址,供老端提示"角色在 xxx 上线";
// ② 清掉 uid 与频道身份——它随后的 Disconnect/Release 不再以这个角色行动:
//    掉线通知因 uid 为空自然跳过,也不会误删新会话刚接手的频道成员。
//
// 老会话的表项不用显式摘:它的键就是这个 uid,马上被新会话覆盖。
// ⚠️ Redis 后端分歧:此处对 values 的修改只落在内存副本、不写穿存储(Session 层
// dirty-key 机制才写穿)。于是被接管会话的存储里 uid 仍在——它持 secret 重连会
// 还原出带 uid 的副本并经 rebind 重新夺回角色(顶掉接管者,"后回来的秘钥赢")。
// 内存后端无此分歧:uid 清掉就是存储里那份,重连落地为未选角(TestReconnectAfterTakeover)。
// ⚠️ 本函数运行时**不持有任何会话锁**(sock.Replaced 会同步 Emit 走业务下发逻辑,
// 塞进 p.Mutex 里迟早撞上重入死锁,与旧 negotiate 的约束一致)。
func supersede(old *session.Data, uid string, neo *session.Data) {
	if sock := Socket(old); sock != nil {
		ip := ""
		if ns := Socket(neo); ns != nil && ns.RemoteAddr() != nil {
			ip = stripPort(ns.RemoteAddr().String())
		}
		sock.Replaced(ip)
	}
	old.Mutex(func(setter session.Setter) {
		setter.Delete(gwcfg.ServiceMetadataUID)
	})
	channel.SwitchUID(old, uid)
}

// Get 按角色ID取在线会话,不在线返回 nil
func Get(uid string) *session.Data {
	v, ok := players.Load(uid)
	if !ok {
		return nil
	}
	p, _ := v.(*session.Data)
	return p
}

func Range(fn func(*session.Data) bool) {
	players.Range(func(k, v any) bool {
		if p, ok := v.(*session.Data); ok {
			return fn(p)
		}
		return true
	})
}

// Delete 下线清理:摘掉本会话的表项并关闭其连接
func Delete(p *session.Data) bool {
	if p == nil {
		return false
	}
	//只有表项仍指向本会话才摘:换角/被接管后同一uid可能已归属别的会话,不能误摘
	if uid := p.GetString(gwcfg.ServiceMetadataUID); uid != "" {
		if v, ok := players.Load(uid); ok && v == p {
			players.Delete(uid)
		}
	}
	sock := Socket(p)
	if sock != nil {
		sock.Close()
	}
	return true
}
