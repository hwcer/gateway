package gwcfg

const (
	ServiceMetadataUID       = "uid"
	ServiceMetadataGUID      = "guid"
	ServiceMetadataOAuth     = "OAuth" //接口等级
	ServiceMetadataDeveloper = "dev"   //开发者身份

	ServiceMetadataCookie       = "cookie"
	ServiceMetadataServerId     = "sid"
	ServiceMetadataRequestId    = "_rk"
	ServiceMetadataResponseType = "_rt"

	ServiceSocketId = "_sock_id"
	ServiceClientIp = "_client_ip"

	ServiceMessagePath   = "_msg_path"
	ServiceMessageRoom   = "_msg_room"
	ServiceMessageIgnore = "_msg_ignore"

	ServicePlayerOAuth   = "_player_oauth"
	ServicePlayerLogout  = "_player_logout"
	ServicePlayerGateway = "_player_gateway"

	ServicePlayerRoomJoin  = "player.join."     //已经加入的房间
	ServicePlayerRoomLeave = "player.leave."    //离开房间
	ServicePlayerSelector  = "service.selector" //服务器重定向

)

type ResponseType string

const (
	ResponseTypeNone      ResponseType = "0" //常规响应
	ResponseTypeRecv                   = "1" //收到推送消息
	ResponseTypeBroadcast              = "2" //收到广播消息
)
