package gwcfg

import (
	"strings"

	"github.com/hwcer/cosgo/registry"
)

// GetServiceMethod 获取外网使用的Method
func GetServiceMethod(method string) string {
	if Gateway.Prefix == "" {
		return method
	}
	return registry.Join(Gateway.Prefix, method)
}

// HasServiceMethod 判断是外网接口
func HasServiceMethod(path string) bool {
	if Gateway.Prefix == "" {
		return true //无法判断
	}
	path = strings.TrimPrefix(path, "/")
	return strings.HasPrefix(path, Gateway.Prefix)
}

func TrimServiceMethod(path string) string {
	if Gateway.Prefix == "" {
		return path
	}
	path = strings.TrimPrefix(path, "/")
	return strings.TrimPrefix(path, Gateway.Prefix)
}

func GetServiceSelectorAddress(k string) string {
	return ServicePlayerSelector + strings.ToLower(k)
}
