package types

import "sync"

type OnlineDevices struct {
	devices map[string]WsMachineInfoRequest
	mutex   sync.RWMutex
}

func NewOnlineDevices() *OnlineDevices {
	return &OnlineDevices{
		devices: make(map[string]WsMachineInfoRequest),
		mutex:   sync.RWMutex{},
	}
}

func (od *OnlineDevices) SetDevice(id string, di WsMachineInfoRequest) {
	od.mutex.Lock()
	od.devices[id] = di
	od.mutex.Unlock()
}

func (od *OnlineDevices) RemoveDevice(id string) {
	od.mutex.Lock()
	delete(od.devices, id)
	od.mutex.Unlock()
}
