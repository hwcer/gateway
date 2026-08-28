package gateway

import (
	"github.com/hwcer/gateway/context"
	"time"

	"github.com/hwcer/cosgo/registry"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/logger"
)

// heartbeat 心跳处理，长连接与短连接共用。
//
// 三档：业务 Handler 实现了 C2SHeartbeat 接口 → 转发到业务服 → 网关自己应答时间戳。
//
// 心跳**必须**送到业务服：玩家对象的心跳戳在那边，收不到就会被玩家容器判定断线，
// 业务侧只好在别的请求上补 keepalive。所以没配 G2SHeartbeat 时按客户端心跳包名原样投递，
// 而不是退化成"不转发"。
//
// ⚠️ 未选角不转发：那时业务服没有玩家对象可刷，而且心跳走默认的 Player 档，
// 转过去也只会被 Access 回 ErrNotSelectRole——白跑一次 RPC。未选角的连接由网关自己保活。
// ⚠️ 转发失败降级为网关应答，不把错误抛给客户端：业务服一抖动，全服连接不该跟着掉。
func heartbeat(c context.Context) any {
	if h, ok := Setting.Handler.(C2SHeartbeat); ok {
		return h.C2SHeartbeat(c)
	}
	p := c.Session() //GetString 不是 nil-safe,必须先判空
	if Setting.G2SHeartbeat != "" && p != nil && p.GetString(gwcfg.ServiceMetadataUID) != "" {
		reply, err := Forward(c, Setting.G2SHeartbeat)
		if err == nil {
			return reply //Response 已由 Forward 过完
		}
		logger.Debug("心跳转发失败,path:%v,err:%v", Setting.G2SHeartbeat, err)
	}
	return time.Now().UnixMilli()
}

// initHeartbeat 启动时校正 Setting.G2SHeartbeat：留空则回落到客户端心跳包名，
// 缺服务名段就补上 gwcfg.ServiceTypeGame，补完仍路由不出来才清空（清空 = 不转发）。
//
// **在启动时定死**而不是每次心跳现算：省掉每个心跳包一次路由解析，更重要的是配不对时
// 立刻能看见——否则只会每次心跳静默降级成网关应答，而角色在业务服那边慢慢被判掉线，
// 现场只留一行 Debug，查起来毫无线索。
//
// ⚠️ 回落值是**客户端包名**（如 "heartbeat"），没有服务名段、Router 解析不出来，
// 所以要补成 "game/heartbeat" 才转发得出去。心跳固定投给游戏服：玩家对象在那儿，
// 别的服拿到也没有心跳戳可刷。业务服若不叫这个名，显式配 G2SHeartbeat 覆盖即可。
func initHeartbeat() {
	if _, ok := Setting.Handler.(C2SHeartbeat); ok {
		return //业务 Handler 自己接管了心跳，不走转发
	}
	if Setting.G2SHeartbeat == "" {
		Setting.G2SHeartbeat = Setting.C2SHeartbeat
	}
	if Setting.G2SHeartbeat == "" {
		return
	}
	if _, _, err := Setting.Handler.Router(Setting.G2SHeartbeat, values.Metadata{}); err == nil {
		return //本来就是完整路径
	}
	//解析不出来通常是缺服务名段：回落值是客户端包名（如 "heartbeat"），
	//补上业务服名就是业务服上的心跳路径（"game/heartbeat"）。
	path := registry.Join(gwcfg.ServiceTypeGame, Setting.G2SHeartbeat)
	if _, _, err := Setting.Handler.Router(path, values.Metadata{}); err != nil {
		logger.Alert("心跳无法转发到业务服(path:%v,%v);玩家心跳戳收不到会被判掉线,请把 Setting.G2SHeartbeat 配成完整的 /服务名/方法", path, err)
		Setting.G2SHeartbeat = "" //置空 = 不转发,退回网关自己应答
		return
	}
	Setting.G2SHeartbeat = path
}
