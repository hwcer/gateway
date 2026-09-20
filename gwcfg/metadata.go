package gwcfg

import (
	"maps"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hwcer/cosgo/phase"
)

const (
	ServiceMetadataUID        = "uid"
	ServiceMetadataGUID       = "guid"
	ServiceMetadataServerId   = "sid"
	ServiceMetadataDeveloper  = "dev" //开发者身份
	ServiceMetadataPermission = "per" //接口等级

	ServiceMetadataGateway   = "_gate"
	ServiceMetadataAddress   = "_addr"
	ServiceMetadataSocketId  = "_sock"
	ServiceMetadataRequestId = "_rid" //Request id

	ServiceMessagePath    = "_msg_path"
	ServiceMessageIgnore  = "_msg_ignore"
	ServiceMessageChannel = "_msg_channel"

	ServicePlayerLogin  = "_player_login"
	ServicePlayerLogout = "_player_logout"
	ServicePlayerCookie = "_player_cookie"

	// 频道命令统一编码:key = 前缀 + ["name","value"](JSON),频道身份由key表达;
	// value 仅 Kick 使用(被踢玩家UID),Join/Leave 为空
	ServicePlayerChannelJoin  = "player.join."     //加入频道
	ServicePlayerChannelLeave = "player.leave."    //离开频道
	ServicePlayerChannelKick  = "player.kick."     //踢出指定玩家,值为被踢玩家UID
	ServicePlayerSelector     = "player.selector." //服务器重定向

	ServiceResponseFlag = "_res_flag" //message flag

)

// metadataReservedExtra 业务层追加的保留键黑名单(经 AddMetadataReserved 设置)。
// 原子指针指向不可变 map(COW):MetadataReserved 在请求热路径上读(每个 query 键
// 一次原子 Load,x86 上即普通 MOV,与普通读同价),AddMetadataReserved 拷贝后整体
// 换指针。🔴 不做 COW 时运行期追加是 concurrent map 读写 fatal(不可 recover,
// 进程直接崩且无 Alert 留痕)——旧契约"仅启动期调用"只由注释承载,违约代价过重
var (
	metadataReservedExtra atomic.Pointer[map[string]struct{}]
	metadataReservedMu    sync.Mutex //写者互斥:并发 Add 时拷贝+换指针必须串行,读者全程无锁
)

// AddMetadataReserved 业务层追加禁止客户端 query 注入的保留键。
//
// 系统内置保留键已默认禁止(见 MetadataReserved);业务自定义的受信键——
// 凡是网关/业务服会**信任其值**的 metadata(自有身份、权限、路由类键)——
// 在此加入黑名单,客户端 query 传了也会被丢弃。
// 🔴 仅启动期调用(包 init 或 Module.Init):封板后调用只 Alert 提示并忽略——
// 守卫读 cosgo 的 phase.Sealed()(公共启动阶段时钟),与 cosgo/session 事件表
// 同一契约。写侧仍带 COW+互斥,即便违约也不会 fatal(race 安全,只是不生效)
func AddMetadataReserved(keys ...string) {
	if phase.Sealed() {
		phase.Alert("gwcfg.AddMetadataReserved(%v)", keys)
		return
	}
	metadataReservedMu.Lock()
	defer metadataReservedMu.Unlock()
	next := make(map[string]struct{}, len(keys))
	if old := metadataReservedExtra.Load(); old != nil {
		maps.Copy(next, *old)
	}
	for _, k := range keys {
		next[k] = struct{}{}
	}
	metadataReservedExtra.Store(&next)
}

// MetadataReserved 判断 metadata key 是否为受信保留键:客户端 query 不得注入,
// 由 Access 层或网关统一回填(黑名单制——未列出的键客户端可自由透传)。
// 默认黑名单:
//   - "_" 前缀的框架内部键(_addr/_sock/_msg_*/_player_* 等),**_rid 除外**(见下)
//   - uid/guid/sid/dev/per(身份、区服、权限类)
//   - player.selector.* 前缀(服务器重定向)
//   - player.join./player.leave./player.kick. 前缀(频道命令)——这些是网关
//     **直接执行**的受信指令(以会话当前身份变更频道归属/Kick 玩家),漏拦时
//     客户端 query 注入即可把任意 uid 踢出频道,与 selector 同级危险
//
// 🔴 例外:_rid 是客户端自行维护的重连对账序号(handledIndex),按契约就是客户端
// 传入的,不得拦——拦掉后 HTTP 请求的 Index() 恒 0,断线重连时客户端会把断线
// 瞬间所有在飞包当未处理重发。TCP 路径不受影响:过滤后网关用帧头序号覆写注入
// _rid(gate_tcp.go),query 传什么都不生效
//
// 🔴 query 全量透传时,非开发者可预置 dev=1 走 GM 类接口、可注入 uid/sid 干扰
// 业务身份判定(access.go 对 OAuth/None 档不覆写这些键)
func MetadataReserved(k string) bool {
	if k == ServiceMetadataRequestId {
		return false //客户端合法传入的重连对账序号,见上方例外说明
	}
	if strings.HasPrefix(k, "_") {
		return true
	}
	switch k {
	case ServiceMetadataUID, ServiceMetadataGUID, ServiceMetadataServerId, ServiceMetadataDeveloper, ServiceMetadataPermission:
		return true
	}
	if strings.HasPrefix(k, ServicePlayerSelector) ||
		strings.HasPrefix(k, ServicePlayerChannelJoin) ||
		strings.HasPrefix(k, ServicePlayerChannelLeave) ||
		strings.HasPrefix(k, ServicePlayerChannelKick) {
		return true
	}
	if extra := metadataReservedExtra.Load(); extra != nil {
		_, ok := (*extra)[k]
		return ok
	}
	return false
}
