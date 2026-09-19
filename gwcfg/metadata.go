package gwcfg

import (
	"strings"
	"sync"
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

// metadataReservedExtra 业务层追加的保留键黑名单(经 AddMetadataReserved 设置)
var (
	metadataReservedMu    sync.RWMutex
	metadataReservedExtra = map[string]struct{}{}
)

// AddMetadataReserved 业务层追加禁止客户端 query 注入的保留键。
//
// 系统内置保留键已默认禁止(见 MetadataReserved);业务自定义的受信键——
// 凡是网关/业务服会**信任其值**的 metadata(自有身份、权限、路由类键)——
// 在此加入黑名单,客户端 query 传了也会被丢弃。启动期调用,支持运行期追加。
func AddMetadataReserved(keys ...string) {
	metadataReservedMu.Lock()
	defer metadataReservedMu.Unlock()
	for _, k := range keys {
		metadataReservedExtra[k] = struct{}{}
	}
}

// MetadataReserved 判断 metadata key 是否为受信保留键:客户端 query 不得注入,
// 由 Access 层或网关统一回填(黑名单制——未列出的键客户端可自由透传)。
// 默认黑名单:
//   - "_" 前缀的框架内部键(_addr/_sock/_rid/_msg_*/_player_*)
//   - uid/guid/sid/dev/per(身份、区服、权限类)
//   - player.selector.* 前缀(服务器重定向)
//
// 🔴 query 全量透传时,非开发者可预置 dev=1 走 GM 类接口、可注入 uid/sid 干扰
// 业务身份判定(access.go 对 OAuth/None 档不覆写这些键)
func MetadataReserved(k string) bool {
	if strings.HasPrefix(k, "_") {
		return true
	}
	switch k {
	case ServiceMetadataUID, ServiceMetadataGUID, ServiceMetadataServerId, ServiceMetadataDeveloper, ServiceMetadataPermission:
		return true
	}
	if strings.HasPrefix(k, ServicePlayerSelector) {
		return true
	}
	metadataReservedMu.RLock()
	defer metadataReservedMu.RUnlock()
	_, ok := metadataReservedExtra[k]
	return ok
}
