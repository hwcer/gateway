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

	ServicePlayerLogin  = "_player_login"
	ServicePlayerLogout = "_player_logout"
	ServicePlayerCookie = "_player_cookie"

	ServicePlayerChannelJoin  = "player.join."     //已经加入的房间
	ServicePlayerChannelLeave = "player.leave."    //离开房间
	ServicePlayerChannelKick  = "player.kick."     //把指定玩家踢出房间,值为[频道值,被踢玩家UID]
	ServicePlayerSelector     = "player.selector." //服务器重定向

	ServiceResponseFlag = "_res_flag" //message flag

)
