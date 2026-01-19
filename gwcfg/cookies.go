package gwcfg

import "github.com/hwcer/cosgo/values"

var Cookies = cookiesAllowableName{}

func init() {
	Cookies.Enable(ServiceMetadataUID)
	Cookies.Enable(ServiceMetadataServerId)
	Cookies.Enable(ServiceMetadataDeveloper)
}

type cookiesAllowableName map[string]struct{}

func (this cookiesAllowableName) Enable(name string) {
	this[name] = struct{}{}
}

func (this cookiesAllowableName) Filter(cookie values.Metadata) values.Values {
	r := values.Values{}
	for k, v := range cookie {
		if _, ok := this[k]; ok {
			r[k] = v
		}
	}
	return r
}
