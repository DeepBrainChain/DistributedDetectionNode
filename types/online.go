package types

import "sync"

type OnlineMachines struct {
	machines map[WsOnlineRequest]WsMachineInfoRequest
	mutex    sync.RWMutex
}

func NewOnlineMachines() *OnlineMachines {
	return &OnlineMachines{
		machines: make(map[WsOnlineRequest]WsMachineInfoRequest),
		mutex:    sync.RWMutex{},
	}
}

func (od *OnlineMachines) SetDevice(id WsOnlineRequest, di WsMachineInfoRequest) {
	od.mutex.Lock()
	od.machines[id] = di
	od.mutex.Unlock()
}

func (od *OnlineMachines) RemoveDevice(id WsOnlineRequest) {
	od.mutex.Lock()
	delete(od.machines, id)
	od.mutex.Unlock()
}
