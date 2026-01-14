package gwcfg

const (
	ServiceMetadataUID       = "uid"
	ServiceMetadataSID       = "sid"
	ServiceMetadataGUID      = "guid"
	ServiceMetadataApi       = "api" //接口等级
	ServiceMetadataDeveloper = "dev" //开发者身份

	ServiceMetadataSocketId     = "_s_id"
	ServiceMetadataClientIp     = "_c_ip"
	ServiceMetadataRequestKey   = "_rk"
	ServiceMetadataResponseType = "_rt" //ResponseType 其中一种

	ServiceMetadataCookie = "cookie"

	ServiceMessagePath    = "_msg_path"
	ServiceMessageChannel = "_msg_channel"
	ServiceMessageIgnore  = "_msg_ignore"

	ServicePlayerLogin   = "_player_login"
	ServicePlayerLogout  = "_player_logout"
	ServicePlayerGateway = "_player_gateway"

	ServicePlayerChannelJoin  = "player.join."     //已经加入的房间
	ServicePlayerChannelLeave = "player.leave."    //离开房间
	ServicePlayerSelector     = "service.selector" //服务器重定向

)

const (
	ResponseTypeNone      = "0" //常规响应
	ResponseTypeReceived  = "1" //收到推送消息
	ResponseTypeBroadcast = "2" //收到广播消息
)
