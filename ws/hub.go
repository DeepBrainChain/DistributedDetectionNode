package ws

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"DistributedDetectionNode/db"
	"DistributedDetectionNode/dbc"
	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"
)

var Hub *hub = nil

type hub struct {
	wg      sync.WaitGroup
	wsConns sync.Map
	do      *delayOffline
}

type cachedOfflineItem struct {
	disconnectTime time.Time
	stakingType    types.StakingType
}

type delayOfflineChanInfo struct {
	machine        types.MachineKey
	disconnectTime time.Time
	stakingType    types.StakingType
}

type delayOffline struct {
	connect   chan delayOfflineChanInfo
	diconnect chan delayOfflineChanInfo
	elements  map[types.MachineKey]cachedOfflineItem
	wg        sync.WaitGroup
	done      chan bool
}

// func NewHub() *hub {
// 	return &hub{
// 		wg:      sync.WaitGroup{},
// 		wsConns: sync.Map{},
// 	}
// }

func InitHub(ctx context.Context) error {
	Hub = &hub{
		wg:      sync.WaitGroup{},
		wsConns: sync.Map{},
		do: &delayOffline{
			connect:   make(chan delayOfflineChanInfo),
			diconnect: make(chan delayOfflineChanInfo),
			elements:  make(map[types.MachineKey]cachedOfflineItem),
			wg:        sync.WaitGroup{},
			done:      make(chan bool),
		},
	}
	if err := db.MDB.ReadDelayOffline(
		ctx,
		func(mk types.MachineKey, t time.Time, st types.StakingType) {
			Hub.do.elements[mk] = cachedOfflineItem{
				disconnectTime: t,
				stakingType:    st,
			}
		},
	); err != nil {
		return err
	}
	go Hub.do.HandleDelayOffline()
	return nil
}

func (h *hub) Wait() {
	h.wg.Wait()
	log.Log.Println("All websocket routine exiting")
	h.do.done <- true
	h.do.wg.Wait()
	ctx, cancel := context.WithTimeout(context.TODO(), 20*time.Second)
	defer cancel()
	// for machine, cdi := range h.do.elements {
	// 	db.MDB.WriteDelayOffline(ctx, machine, cdi.disconnectTime)
	// }
	db.MDB.WriteAllDelayOffline(ctx, time.Now())
}

func (do *delayOffline) HandleDelayOffline() {
	ticker := time.NewTicker(10 * time.Second)
	defer func() {
		ticker.Stop()
		close(do.connect)
		close(do.diconnect)
	}()
	for {
		select {
		case item := <-do.connect:
			delete(do.elements, item.machine)
		case item := <-do.diconnect:
			do.elements[item.machine] = cachedOfflineItem{
				disconnectTime: item.disconnectTime,
				stakingType:    item.stakingType,
			}
		case <-ticker.C:
			expired := time.Now().Add(-5 * time.Minute)
			for machine, cdi := range do.elements {
				if cdi.disconnectTime.Before(expired) {
					go do.Offline(delayOfflineChanInfo{
						machine:        machine,
						disconnectTime: cdi.disconnectTime,
						stakingType:    cdi.stakingType,
					})
					delete(do.elements, machine)
				}
			}
		case <-do.done:
			return
		}
	}
}

func (do *delayOffline) Offline(info delayOfflineChanInfo) {
	do.wg.Add(1)
	defer do.wg.Done()
	ctx1, cancel1 := context.WithTimeout(context.TODO(), 60*time.Second)
	defer cancel1()
	if hash, err := dbc.DbcChain.Report(
		ctx1,
		types.MachineOffline,
		info.stakingType,
		info.machine.Project,
		info.machine.MachineId,
	); err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": info.machine,
		}).Errorf(
			"machine offline in chain contract failed with hash %v because of %v",
			hash,
			err,
		)
	} else {
		log.Log.WithFields(logrus.Fields{
			"machine": info.machine,
		}).Info("machine offline in chain contract success with hash ", hash)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	db.MDB.WriteDelayOffline(ctx2, info.machine, time.Now())
}
