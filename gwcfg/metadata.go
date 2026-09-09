package gwcfg

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
