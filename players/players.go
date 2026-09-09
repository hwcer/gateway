package players

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"
)

// players 会话表:**键 = UID(角色ID),只收已选角的会话**。
//
// 认证(登录)阶段的会话不进表——它们住在 socket.data(TCP/WSS)或 cookie/存储(HTTP),
// 没有任何"按账号查会话"的需求:**同账号多角色并行在线是合法状态**。
// 顶号因此是 UID 级而不是账号级:登录不踢任何人,角色占用在选角回包落地时处理(见 rebind)。
//
// 长连接上的登录是**两段式**:
//
//	认证成功   → Auth:仅把 GUID 绑到 socket.Data(内存态,不落存储、不发秘钥)——
//	             这不是 LOGIN,请求鉴权照常走 OAuth 档,角色级接口被 ErrNotSelectRole 挡住
//	选角回包落地 → Login:升级为正式会话(落存储+换不透明id+绑定连接,此刻才下发
//	             重连秘钥);uid cookie 随之经 Update→rebind 入表
var players = sync.Map{}

// Logged 会话是否已完成正式登录(LOGIN)。
// 判据是会话持有秘钥:正式会话必经 Create→Refresh,秘钥一定存在;认证态伪会话
// 不落存储、永不 Refresh——这个判据同时把 Redis 还原副本也判为已登录(存储里有秘钥)。
func Logged(p *session.Data) bool {
	return p != nil && p.GetString(session.TokenSecretName) != ""
}

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
	if token, err = ss.New(data); err != nil {
		return
	}
	//秘钥写穿存储:Refresh 只改内存副本+标脏,不 Release 的话 Redis 后端里它
	//永远不落盘——重连 Verify 还原出的副本没有秘钥,直接 ErrorSessionIllegal。
	//内存后端的 Storage.Update 是 no-op,行为不变
	ss.Release()
	return
}

// Auth 认证成功:仅把 GUID 绑到 socket.Data,**这不是 LOGIN**——
// 不建会话、不落存储、不下发秘钥。正式会话要等选角回包落地(见 Login),
// 这一段里连接上只有账号身份:请求鉴权照常(OAuth 档),角色级接口被
// ErrNotSelectRole 挡住,心跳由网关自己应答。
// 同一连接重复认证同一账号幂等(沿用现有伪会话),换账号重登报错。
func Auth(sock *cosnet.Socket, guid string, value values.Values) (*session.Data, error) {
	if p := sock.Data(); p != nil {
		if p.GetString(gwcfg.ServiceMetadataGUID) != guid {
			return nil, fmt.Errorf("please do not login again")
		}
		return p, nil
	}
	p := session.NewData(guid, value) //id=guid 仅日志可读,不落存储
	p.Set(gwcfg.ServiceMetadataGUID, guid)
	//Authentication 事件照发(S2CSecret 对未落库会话自然跳过),连接从此带上身份
	sock.Authentication(p)
	return p, nil
}

// Login 选角回包落地 = 正式 LOGIN:认证态伪会话升级为正式会话——
// 落存储、换不透明 id、绑定当前连接(重连秘钥此刻才经 S2CSecret 事件下发)。
// 认证期累积在伪会话上的身份信息(developer、oauth 回包带的 cookies)全部带走。
// 入表不在本函数:随后的 uid cookie 经 Update→rebind 完成。
func Login(sock *cosnet.Socket, p *session.Data) (data *session.Data, err error) {
	guid := p.GetString(gwcfg.ServiceMetadataGUID)
	_, data, err = Create(guid, p.Values())
	if err != nil {
		return nil, err
	}
	Replace(data, sock)
	return data, nil
}

// Update 更新会话数据;uid 发生变化(首次选角/换角)时同步维护会话表
func Update(p *session.Data, vs values.Values) {
	if p == nil || len(vs) == 0 {
		return
	}
	//old/uid 成对读进同一把锁:同会话并发的两次换角不会拿到错位的基线
	var old, uid string
	p.Mutex(func(setter session.Setter) {
		old = setter.GetString(gwcfg.ServiceMetadataUID)
		setter.Update(vs)
		uid = setter.GetString(gwcfg.ServiceMetadataUID)
	})
	if uid != old {
		rebind(p, old, uid)
	}
}

// rebind uid 变化(首次选角/换角)时维护会话表。uid 由调用方在会话锁内读好传入。
//
// 表键=uid,一个角色同一时间只允许一个在线会话:新 uid 已被别的会话占着时,
// 本会话**接管**(supersede 老会话)。这就是 UID 级顶号,触发点是选角回包落地——
// 网关不解析协议体,选角之前无从知道目标角色,所以登录阶段不可能做占用判断。
//
// 推送/响应的 metadata 反复携带同一 uid 是常态,uid 未变的快路径在 Update 的
// 锁内就地短路,走不到这里。
func rebind(p *session.Data, oldUID, uid string) {
	//认证态伪会话不进表:它没有存储背书,重连/按uid推送都不成立。正常流程到不了
	//这里(选角回包在 forward 里先升级成正式会话、uid 才落表),纯防御
	if !Logged(p) {
		return
	}
	//先原子换入表项,再处置被挤出的会话:
	//  - 换入到处置完成之间,按 uid 的推送已经落在新会话上,不会多指向老会话一小段;
	//  - supersede 里的 sock.Replaced 会同步 Emit 走业务下发,期间表项必须已是新会话;
	//  - 并发抢同一 uid 的两次接管由 Swap 天然仲裁:后入者赢,先入者作为被挤出方处置,
	//    不会出现"两个会话都自认为持有角色"的窗口。
	var displaced *session.Data
	if uid != "" {
		if prev, loaded := players.Swap(uid, p); loaded {
			//同会话的不同实例不算被挤出(Redis 后端每次 Verify 都从存储新建副本,
			//重连即此形态):这是同一个客户端自己回来,老副本等着废弃即可,
			//不许 Replaced 自己、也不许清自己的 uid 与频道身份
			if os, _ := prev.(*session.Data); os != nil && !os.Is(p) {
				displaced = os
			}
		}
	}
	if oldUID != "" {
		//归属校验的原子版:只有表项仍指向本会话才摘,不能误摘别人的表项
		players.CompareAndDelete(oldUID, p)
		//旧角色的频道身份一并清理,否则换角后旧频道的广播还会推给新角色
		channel.SwitchUID(p, oldUID)
	}
	if displaced != nil {
		supersede(displaced, uid, p)
	}
}

// supersede 新会话接管 uid 时对老会话的处置:
//
// ① 连接进"只收不发"存活期(在途回包照常送达、新请求被拒、到期断开),
//    ip 是新端的地址,供老端提示"角色在 xxx 上线";
// ② 清掉 uid 与频道身份——它随后的 Disconnect/Release 不再以这个角色行动:
//    掉线通知因 uid 为空自然跳过,也不会误删新会话刚接手的频道成员。
//
// 老会话的表项不用显式摘:rebind 的 Swap 已经把它换成了新会话。
// uid 清除必须**写穿存储**(Session 层脏键机制,存空串与删除等价):Redis 后端
// 每次 Verify 都从存储新建副本,只改内存的话被接管会话的存储里 uid 仍在,
// 它持 secret 重连会还原出带 uid 的副本并经 rebind 重新夺回角色,与内存后端
// 相悖(内存后端单实例,改内存即改存储)。内存后端的 Storage.Update 是 no-op,
// 行语义不变。
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
	ss := session.New(old)
	ss.Update(values.Values{gwcfg.ServiceMetadataUID: ""})
	ss.Release()
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
	//只有表项仍指向本会话才摘(原子归属校验):换角/被接管后同一uid可能已归属
	//别的会话,不能误摘
	if uid := p.GetString(gwcfg.ServiceMetadataUID); uid != "" {
		players.CompareAndDelete(uid, p)
	}
	sock := Socket(p)
	if sock != nil {
		sock.Close()
	}
	return true
}
