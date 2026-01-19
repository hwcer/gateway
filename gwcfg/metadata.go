package gwcfg

const (
	ServiceMetadataUID       = "uid"
	ServiceMetadataGUID      = "guid"
	ServiceMetadataServerId  = "sid"
	ServiceMetadataDeveloper = "dev"   //开发者身份
	ServiceMetadataAuthorize = "OAuth" //接口等级

	ServiceMetadataSocketId     = "_s_id"
	ServiceMetadataClientIp     = "_c_ip"
	ServiceMetadataRequestId    = "_r_id" //Request id
	ServiceMetadataResponseType = "_r_t"  //ResponseType 其中一种,仅仅内部使用
	ServiceMetadataGateway      = "_g_w"
	ServiceMetadataCookie       = "cookie"

	ServiceMessagePath    = "_msg_path"
	ServiceMessageIgnore  = "_msg_ignore"
	ServiceMessageChannel = "_msg_channel"

	ServicePlayerLogin  = "_player_login"
	ServicePlayerLogout = "_player_logout"

	ServicePlayerChannelJoin  = "player.join."     //已经加入的房间
	ServicePlayerChannelLeave = "player.leave."    //离开房间
	ServicePlayerSelector     = "player.selector." //服务器重定向

)

const (
	ResponseTypeNone      = "0" //常规响应
	ResponseTypeReceived  = "1" //收到推送消息
	ResponseTypeBroadcast = "2" //收到广播消息
)
