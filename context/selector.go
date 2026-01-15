package context

import "github.com/hwcer/gateway/gwcfg"

func NewSelector(c Context) *Selector {
	return &Selector{Context: c}
}

type Selector struct {
	Context
}

func (this *Selector) Set(k, v string) {
	name := gwcfg.GetServiceSelectorAddress(k)
	this.SetMetadata(name, v)
}

func (this *Selector) Remove(k string) {
	name := gwcfg.GetServiceSelectorAddress(k)
	this.SetMetadata(name, "")
}
