package players

import (
	"context"
	"sync"
	"time"

	"github.com/hwcer/cosgo/scc"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/logger"
)

// Sweeper 掉线会话清理器。
//
// 🔴 背景:TCP 掉线路径(Disconnect)只解绑 socket,不产生 EventSessionRelease——
// Redis 后端唯一 Release 源是 HTTP logout。长连接玩家退出后:会话表项(uid→无连接
// 会话)与频道成员永久滞留,内存无界增长,broadcast 随历史在线人数线性恶化。
//
// 策略:定期扫描会话表,"断线且超过存活期"的会话走标准 Release(表项摘除、频道
// 释放、事件触发)。保留期与断线重连的语义衔接:断线会话先留一个重连窗口,到期
// 才认定玩家真的走了。断线时刻以 socket 绑定消失为界(不精确到秒,清理粒度=tick)。
var Sweeper = struct {
	// Interval 扫描间隔(秒),<=0 关闭清理器(默认开启,60s)
	Interval int32
	// Grace 断线会话保留期(秒):socket 已解绑且超过该时长的会话被清理。
	// 应 ≥ cosnet Options.SocketConnectTime(顶号协商期)与业务重连窗口
	Grace int32
}{Interval: 60, Grace: 300}

// sweeperLastOffline 断线时刻记录:会话指针 → 断线时间。
// 会话重连(Replace 回写 socket)或被清理时移除;Map 不随会话数量收缩,
// 但条目数 ≤ 历史断线会话中被复用的数量,量级可控
var sweeperLastOffline sync.Map // map[*session.Data]time.Time

func init() {
	session.On(session.EventSessionRelease, func(i any) {
		if data, ok := i.(*session.Data); ok {
			sweeperLastOffline.Delete(data)
		}
	})
}

// StartSweeper 启动掉线清理协程(模块 Start 期调用一次;Interval<=0 不启动)
func StartSweeper() {
	if Sweeper.Interval <= 0 {
		return
	}
	scc.CGO(func(ctx context.Context) {
		ticker := time.NewTicker(time.Duration(Sweeper.Interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	})
	logger.Debug("players sweeper started, interval:%ds grace:%ds", Sweeper.Interval, Sweeper.Grace)
}

func sweep() {
	grace := time.Duration(Sweeper.Grace) * time.Second
	now := time.Now()
	Range(func(p *session.Data) bool {
		sock := Socket(p)
		if sock != nil && sock.IsReady() {
			//在线:清掉可能残留的断线记录
			sweeperLastOffline.Delete(p)
			return true
		}
		//Closing(顶号协商期)是活连接的正常阶段,不算断线;
		//CanWrite 覆盖 Connected|Closing 两态
		if sock != nil && sock.CanWrite() {
			return true
		}
		//断线(sock==nil 或已死):记录或比对断线时刻
		if t, ok := sweeperLastOffline.Load(p); !ok {
			sweeperLastOffline.Store(p, now)
			return true
		} else if now.Sub(t.(time.Time)) < grace {
			return true //重连窗口内,保留
		}
		//超时:走标准 Release(EventSessionRelease → release 钩子:摘表项+频道释放)
		if uid := p.GetString(gwcfg.ServiceMetadataUID); uid != "" {
			logger.Debug("sweeper release offline session, uid:%s", uid)
		} else {
			logger.Debug("sweeper release offline session(never selected)")
		}
		if ss := session.New(p); ss.Data != nil {
			//会话级 Delete:清存储键、触发 EventSessionRelease(gateway.release 摘表项+频道)
			_ = ss.Delete()
		} else {
			//兜底:存储不可用等异常时至少清网关侧状态
			players.Delete(p)
			channel.Release(p)
		}
		sweeperLastOffline.Delete(p)
		return true
	})
}
