package gwcfg

const (
	ServiceMetadataUID        = "uid"
	ServiceMetadataGUID       = "guid"
	ServiceMetadataServerId   = "sid"
	ServiceMetadataDeveloper  = "dev" //开发者身份
	ServiceMetadataPermission = "per" //接口等级

	ServiceMetadataSocketId  = "sock"
	ServiceMetadataGateway   = "gate"
	ServiceMetadataClientIp  = "_uip"
	ServiceMetadataRequestId = "_rid" //Request id

	ServiceMessagePath    = "_msg_path"
	ServiceMessageIgnore  = "_msg_ignore"
	ServiceMessageChannel = "_msg_channel"

	ServicePlayerLogin  = "_player_login"
	ServicePlayerLogout = "_player_logout"
	ServicePlayerCookie = "_player_cookie"

	ServicePlayerChannelJoin  = "player.join."     //已经加入的房间
	ServicePlayerChannelLeave = "player.leave."    //离开房间
	ServicePlayerSelector     = "player.selector." //服务器重定向

	ServiceResponseModel = "_res_mod" //ResponseType 其中一种,仅仅内部使用

)

const (
	ResponseTypeNone      = "0" //常规响应
	ResponseTypeReceived  = "1" //收到推送消息
	ResponseTypeBroadcast = "2" //收到广播消息,一次广播只调用一次，session.Data 为空(调用时不针对任何用户，仅仅处理包体)
)
