package players

import (
	"maps"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/logger"
)

// instanceKey 会话实例标识:每次登录生成、随会话持久化。
// 🔴 Redis 后端所有同 guid 会话的 Data.id 相同(reid=guid),顶号仲裁无法区分
// "同一客户端重连"与"另一台设备登录"——supersede 整个被跳过,双设备短窗内
// 共享同一角色。重连 Verify 还原的副本携带同一标识,幂等回表不受影响
const instanceKey = "_inst"

// players 会话表:**键 = UID(角色ID),只收已选角的会话**。
//
// 会话本身自认证起就存在(id=GUID,见 Create),但认证阶段没有角色,不入表——
// 频道、踢人、推送等业务语义都按 UID 说话,表键即业务键,按角色定位一次直查,
// 不存在"查错人"的结构性风险(旧实现的 UID->GUID 反向映射与归属校验因此整个不需要)。
// 同账号多角色并行在线是合法状态:会话按登录建,不按账号复用。
//
// 顶号因此是 UID 级而不是账号级:登录不踢任何人,角色占用在选角回包落地时处理
// (见 rebind);强制还是协商由网关层 negotiate(Setting.ForceReplace)在落地前决定。
var players = sync.Map{}

// Create 认证登录:新建会话(id=guid)写入存储并返回 token。**不进会话表**——
// 表键是 uid,角色要等选角回包落地(Update→rebind)才入表。
//
// 会话 id 与账号身份的关系随存储后端不同:Redis 后端保留自定义 id(id=guid);
// 内存后端的 storage.New 会把它重写成分配的 token。因此**账号身份一律以
// session.Data.UUID() 为准**(与主干语义一致),不另存进 values——values 里的
// 键可经 Cookies 白名单被业务回包改写,身份键放那里有被改写的风险。
func Create(guid string, value values.Values) (token string, data *session.Data, err error) {
	//🔴 克隆后再写实例标识:value 会被 session.NewData 按引用持有为会话 values,
	//不能原地改调用方的 map;实例标识每次登录重新生成,随会话落库——重连副本
	//据此识别"同一个客户端自己回来了"
	vs := make(values.Values, len(value)+1)
	maps.Copy(vs, value)
	vs[instanceKey] = strconv.FormatInt(time.Now().UnixNano(), 36) + strconv.FormatInt(rand.Int63(), 36)
	ss := session.NewWithValues(guid, vs)
	data = ss.Data
	if token, err = ss.New(data); err != nil {
		return
	}
	//秘钥写穿存储:库的 New 先落库、之后才生成秘钥,而 Refresh 只改内存+标脏,
	//不 Submit 的话 Redis 后端里秘钥永远不落盘——重连 Verify 还原出的副本没有
	//秘钥,直接 ErrorSessionIllegal。内存后端同一实例,行为不变
	err = ss.Submit()
	return
}

// Update 更新会话数据;uid 发生变化(首次选角/换角)时同步维护会话表
func Update(p *session.Data, vs values.Values) {
	if p == nil || len(vs) == 0 {
		return
	}
	// 换角时旧角色要走一遍**模拟登出**(仅网关层面触发事件):在 uid 翻转之前、
	// 以旧身份补一发掉线事件——业务侧监听 EventSessionDisconnect,按事件时刻
	// 会话上的 uid 感知旧角色下线,与真实掉线同一条路。连接与会话都保留给新角色;
	// 旧角色的表项摘除、频道清理由随后的 rebind 完成(相当于真实登出时 Release
	// 阶段的清理,只是按旧角色收窄)。被顶号(supersede)不发:角色还在线,只是
	// 换了持有者。
	// ⚠️ 事件回调同步走业务逻辑,不能在会话锁内发(重入死锁,同 supersede 约束),
	// 所以按"先判变→锁外发事件→锁内落值"的顺序。uid 只能经 vs 里的同名键变化
	//(Update 是平铺合并),锁外的判变是精确的;键不存在不发(常态响应反复携带
	// 同值 uid 也不发),显式清空(uid 变空,退回未选角)同样算旧角色登出。
	if v, ok := vs[gwcfg.ServiceMetadataUID]; ok {
		next, _ := v.(string)
		if old := p.GetString(gwcfg.ServiceMetadataUID); old != "" && old != next {
			session.Emit(session.EventSessionDisconnect, p)
		}
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
	writeThrough(p, vs)
}

// UpdateExpect 推送路径的会话数据更新：expectUID 为调用方定位会话时依据的 uid 基线。
//
// 推送从定位到落地之间隔着换角/顶号窗口：会话可能已翻到新角色或被清空身份。
// 锁内发现会话当前 uid 已不等于基线时，丢弃 vs 中的 uid 键、只应用其余 cookies——
// **推送只投递、不变更身份**：既不能把会话翻回旧角色（在飞推送 vs 换角 rebind 的
// 竞态会让网关与业务服身份分叉），也不能在顶号窗口帮被顶掉的僵尸会话经 rebind
// 夺回表项、把新端置入只收不发。
//
// 基线校验必须在会话锁内做：锁外读到的 uid 与落地之间同样隔着并发窗口，等于没校验。
func UpdateExpect(p *session.Data, vs values.Values, expectUID string) {
	if p == nil || len(vs) == 0 {
		return
	}
	p.Mutex(func(setter session.Setter) {
		if setter.GetString(gwcfg.ServiceMetadataUID) != expectUID {
			delete(vs, gwcfg.ServiceMetadataUID)
		}
		setter.Update(vs)
	})
	writeThrough(p, vs)
}

// writeThrough cookies 全量写穿存储(幂等,失败仅告警)。
//
// 🔴 uid 由 rebind 单独写穿,这里跳过防重复 HMSET;其余键(_rid/sid/selector 等)
// 旧实现只落内存副本——Redis 后端断线重连 Verify 从存储新建副本时全部丢失:
// 重连对账的 handledIndex 取 _rid,缺失则永远回 0,客户端把断线瞬间所有在飞包
// 当未处理重发(业务重复执行);selector 丢失则转发投错区服。
func writeThrough(p *session.Data, vs values.Values) {
	filtered := make(values.Values, len(vs))
	for k, v := range vs {
		if k == gwcfg.ServiceMetadataUID {
			continue
		}
		filtered[k] = v
	}
	if len(filtered) == 0 {
		return
	}
	ss := session.New(p)
	ss.Update(filtered)
	if err := ss.Submit(); err != nil {
		logger.Alert("players write through error:%v", err)
	}
}

// rebind uid 变化(首次选角/换角)时维护会话表。uid 由调用方在会话锁内读好传入。
//
// 表键=uid,一个角色同一时间只允许一个在线会话:新 uid 已被别的会话占着时,
// 本会话**接管**(supersede 老会话)。这就是 UID 级顶号,触发点是选角回包落地——
// 网关不解析协议体,选角之前无从知道目标角色,所以登录阶段不可能做占用判断。
//
// 强制还是协商不在这里决定:forward 在 uid 落地前先过 negotiate(见 replaced.go,
// 按 Setting.ForceReplace),被拒根本到不了这里——走到这里的都是已放行的接管。
// Reconnect 路径不经协商,但它只会幂等地回到自己原有的表项,不构成顶号。
//
// 推送/响应的 metadata 反复携带同一 uid 是常态,uid 未变的快路径在 Update 的
// 锁内就地短路,走不到这里。
func rebind(p *session.Data, oldUID, uid string) {
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
			//不许 Replaced 自己、也不许清自己的 uid 与频道身份。
			//判等依据是实例标识而非 Data.id:Redis 后端 id=guid,同 guid 会话的
			//id 恒等,旧判等恒真,supersede 被跳过,双设备短窗共享同一角色
			if os, _ := prev.(*session.Data); os != nil {
				//🔴 被换出的副本从此不再被 sweeper 扫到(表项已指向新会话),
				//它的断线登记随手清掉,否则条目随"断线后被复用"的会话数
				//缓慢累积(Redis 后端每次重连都产生新副本,是常态路径)
				sweeperLastOffline.Delete(os)
				if !sameInstance(os, p) {
					displaced = os
				}
			}
		}
	}
	//uid 变更**无条件**写穿存储(含清空):HTTP+Redis 下会话每个请求都从存储
	//新建副本,uid 不落盘的话下一个请求就回到旧值——换角到空若不写穿,还原出的
	//副本仍持旧 uid 而表项已摘,按 uid 推送静默丢失。存空串与删除等价;
	//内存后端的 Storage.Update 是 no-op
	ss := session.New(p)
	ss.Update(values.Values{gwcfg.ServiceMetadataUID: uid})
	ss.Submit()
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

// sameInstance 判断两个会话是否为同一客户端实例:比对实例标识而非 Data.id
// (Redis 后端同 guid 会话 id 恒等)。标识缺失(升级前的旧会话)按不同实例处理。
// 🔴 读必须进会话锁:标识本身在 Create/Verify 后不再变更,但 values 是并发写的
// (supersede 清 uid 等),cosgo 的 Data 读操作不带锁——锁外裸读是数据竞争
// (-race 实报,TestRebindConcurrentSameUID)。两把锁顺序获取、不同时持有,无死锁面
func sameInstance(a, b *session.Data) bool {
	var ia, ib string
	a.Mutex(func(s session.Setter) {
		ia = s.GetString(instanceKey)
	})
	b.Mutex(func(s session.Setter) {
		ib = s.GetString(instanceKey)
	})
	return ia != "" && ia == ib
}

// UIDIs 会话当前 uid 是否等于基线(锁内读)。推送路径据此判定"定位到落地之间
// 身份是否已翻变",翻变时不得再执行变更身份/频道的命令
func UIDIs(p *session.Data, uid string) bool {
	if p == nil || uid == "" {
		return false
	}
	var cur string
	p.Mutex(func(s session.Setter) {
		cur = s.GetString(gwcfg.ServiceMetadataUID)
	})
	return cur == uid
}

// supersede 新会话接管 uid 时对老会话的处置:
//
// ① 连接进"只收不发"存活期(在途回包照常送达、新请求被拒、到期断开),
//
//	ip 是新端的地址,供老端提示"角色在 xxx 上线";
//
// ② 清掉 uid 与频道身份——它随后的 Disconnect/Release 不再以这个角色行动:
//
//	掉线通知因 uid 为空自然跳过,也不会误删新会话刚接手的频道成员。
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
	ss.Submit()
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
