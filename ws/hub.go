package ws

import "sync"

var Hub *hub = nil

type hub struct {
	wg      sync.WaitGroup
	wsConns sync.Map
}

func NewHub() *hub {
	return &hub{
		wg:      sync.WaitGroup{},
		wsConns: sync.Map{},
	}
}

func InitHub() {
	Hub = &hub{
		wg:      sync.WaitGroup{},
		wsConns: sync.Map{},
	}
}

func (h *hub) Wait() {
	h.wg.Wait()
}
