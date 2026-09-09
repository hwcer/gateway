package players

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
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
// 长连接与短连接统一为**两段式登录**,认证都不算 LOGIN:
//
//	认证成功     TCP:Auth 仅把 GUID 绑到 socket.Data(内存态,不落存储、不发秘钥);
//	             HTTP:CreateAuth 建认证态会话(含秘钥供 cookie 鉴权)但无角色,
//	             永不入会话表
//	选角回包落地  Login 升级为正式会话:落存储+绑定连接,长连接经 S2CSecret 下发
//	             新秘钥、HTTP 换发 Set-Cookie。uid cookie 经 Update→rebind 入表
//
// 🔴 两阶段靠 values 里的 ServiceMetadataAuthStage 标记区分,**不能用 id 格式判**:
// 内存后端的 storage.New 会把自定义 id 重写成分配的 token(NewMemorySetter
// d.id=id),guid/随机段这类约定在内存后端根本存活不了。
var players = sync.Map{}

// authPending 认证态会话记账(guid → 会话):HTTP 每次认证建新前先删旧——
// 同一账号任意时刻至多一条活跃认证态会话,无限刷认证也刷不出多条。
// 内存后端键不可控(storage 重写 id),防刷只能靠这里;Redis 后端另有确定性 id
// 覆盖(见 authID),这里是顺带把本进程的旧条目清掉
var authPending = sync.Map{}

// authID 认证态会话在 Redis 后端下的确定性 id:纯 guid——同账号重复认证
// HMSET 覆盖同一条,跨网关进程也防得住刷。guid 里的 "/" 转义,避免与历史
// 会话 id(guid/随机段)格式混淆
func authID(guid string) string {
	return strings.ReplaceAll(guid, "/", "%2F")
}

// AuthStage 会话是否仍处于认证阶段(未 LOGIN)。认证态不入会话表、不参与接管
func AuthStage(p *session.Data) bool {
	return p != nil && p.GetInt32(gwcfg.ServiceMetadataAuthStage) > 0
}

// Persistent 会话是否已落库且持有秘钥(可被 token 复原)。TCP 认证态伪会话不落
// 存储,恒为 false——S2CSecret 据此跳过(否则会在伪会话上现生成不落库的废秘钥);
// HTTP 认证会话与正式会话都是 true
func Persistent(p *session.Data) bool {
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

// Create 新建正式会话(不透明 id=guid/随机段)并写入存储,返回 token。**不进会话表**。
//
// 调用方:Login(选角落地的升级)与 WSS token 失效后的回退(Connect)。
// guid 降级为会话 values 里的普通字段(ServiceMetadataGUID),业务侧经转发 metadata
// 照常拿到。角色要等 uid cookie 经 Update→rebind 落地才进表。
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

// CreateAuth 认证登录(HTTP):建认证态会话——含秘钥(cookie 鉴权需要)但不含角色,
// 永不入会话表;选角回包落地时经 Login 升级为正式会话,本条随之删除
//(老 token 一并作废,新 token 由 retoken/S2CSecret 换发)。
//
// 防刷:同账号任意时刻至多一条活跃认证态会话——建新前先删旧(authPending 记账,
// 内存后端 storage 会重写 id、键不可控,只能靠记账);Redis 后端另有确定性 id
//(authID)跨进程覆盖同一条,双保险。
func CreateAuth(guid string, value values.Values) (token string, data *session.Data, err error) {
	if value == nil {
		value = values.Values{}
	}
	value.Set(gwcfg.ServiceMetadataGUID, guid)
	value.Set(gwcfg.ServiceMetadataAuthStage, int32(1))
	data = session.NewData(authID(guid), value)
	ss := session.New(data)
	if token, err = ss.New(data); err != nil {
		return
	}
	ss.Release() //秘钥写穿(同 Create)
	//记账:同账号只保留最新一条,并发认证靠 CAS 重试兜住
	for {
		actual, loaded := authPending.LoadOrStore(guid, data)
		if !loaded {
			break
		}
		old, _ := actual.(*session.Data)
		if old == nil || old == data {
			break
		}
		session.New(old).Delete()
		if authPending.CompareAndSwap(guid, old, data) {
			break
		}
	}
	return
}
// Auth 认证成功(TCP):仅把 GUID 绑到 socket.Data,**这不是 LOGIN**——
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
	p := session.NewData(authID(guid), value) //不落存储,id 仅日志可读
	p.Set(gwcfg.ServiceMetadataGUID, guid)
	p.Set(gwcfg.ServiceMetadataAuthStage, int32(1))
	//Authentication 事件照发(S2CSecret 对未落库会话自然跳过),连接从此带上身份
	sock.Authentication(p)
	return p, nil
}

// Login 选角回包落地 = 正式 LOGIN:认证态会话(TCP 内存伪会话/HTTP 存储认证会话)
// 升级为不透明 id 的正式会话——落存储、绑定当前连接(长连接的新秘钥此刻才经
// S2CSecret 事件下发,HTTP 由调用方用返回的 token 换发 Set-Cookie)。
// 认证期累积的身份信息(developer、oauth cookies)全部带走;HTTP 的认证态存储
// 条目随之删除,老 token 复原不出任何东西。
// 入表不在本函数:随后的 uid cookie 经 Update→rebind 完成。
func Login(sock *cosnet.Socket, p *session.Data) (data *session.Data, token string, err error) {
	guid := p.GetString(gwcfg.ServiceMetadataGUID)
	value := p.Values()
	delete(value, gwcfg.ServiceMetadataAuthStage) //剥认证标记:升级产物是正式会话
	token, data, err = Create(guid, value)
	if err != nil {
		return nil, "", err
	}
	//删认证态条目与记账:TCP 伪会话不在存储,Delete 即 no-op
	session.New(p).Delete()
	authPending.Delete(guid)
	Replace(data, sock) //sock 为 nil(HTTP)时跳过连接绑定
	return data, token, nil
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
	//认证态会话不进表:它不是 LOGIN 的产物,按 uid 推送/接管对它都不成立。
	//正常流程到不了这里(选角回包在 forward 里先升级成正式会话、uid 才落表),纯防御
	if AuthStage(p) {
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
		//uid 变更写穿存储:HTTP+Redis 下会话每个请求都从存储新建副本,uid 不落盘
		//的话下一个请求就回到未选角。选角/换角是稀有事件,这点写放大可忽略;
		//内存后端的 Storage.Update 是 no-op
		ss := session.New(p)
		ss.Update(values.Values{gwcfg.ServiceMetadataUID: uid})
		ss.Release()
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
