package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"DistributedDetectionNode/db"
	"DistributedDetectionNode/dbc"
	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"
)

type Hub struct {
	wg      sync.WaitGroup
	wsConns sync.Map
	do      *delayOffline
	open    atomic.Bool
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
	notifyApi string
}

func InitHub(ctx context.Context, napi string) (*Hub, error) {
	hub := &Hub{
		wg:      sync.WaitGroup{},
		wsConns: sync.Map{},
		do: &delayOffline{
			connect:   make(chan delayOfflineChanInfo),
			diconnect: make(chan delayOfflineChanInfo),
			elements:  make(map[types.MachineKey]cachedOfflineItem),
			wg:        sync.WaitGroup{},
			done:      make(chan bool),
			notifyApi: napi,
		},
	}
	if err := db.MDB.ReadDelayOffline(
		ctx,
		func(mk types.MachineKey, t time.Time, st types.StakingType) {
			hub.do.elements[mk] = cachedOfflineItem{
				disconnectTime: t,
				stakingType:    st,
			}
		},
	); err != nil {
		return nil, err
	}
	go hub.do.HandleDelayOffline()
	go hub.runMachineInfoCheck(ctx)
	hub.open.Store(true)
	return hub, nil
}

// runMachineInfoCheck 每 2 分钟检查一次连接中但缺少硬件信息的机器
func (h *Hub) runMachineInfoCheck(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if h.closed() {
				return
			}
			h.checkMissingMachineInfo(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (h *Hub) Close() {
	h.open.Store(false)
	h.wsConns.Range(func(key, value any) bool {
		if conn, ok := key.(*Client); ok {
			conn.WriteEnvelope(envelope{t: websocket.CloseMessage, msg: []byte("server exiting")})
			// conn.WriteEnvelope(envelope{t: websocket.CloseMessage, msg: websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")})
			conn.conn.Close()
		}
		return true
	})
	log.Log.Println("Shutdownd all websocket connections")
}

func (h *Hub) closed() bool {
	return !h.open.Load()
}

func (h *Hub) Wait() {
	h.wg.Wait()
	log.Log.Println("All websocket routine exiting")
	h.do.done <- true
	h.do.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db.MDB.WriteAllDelayOffline(ctx, time.Now())
}

func (h *Hub) SendUnregisterNotify(machine types.MachineKey, stakingType types.StakingType) {
	h.wsConns.Range(func(key, value any) bool {
		if client, ok := key.(*Client); ok {
			if client.MachineKey == machine && client.StakingType == stakingType {
				notify := types.WsNotifyMessage{
					Unregister: types.WsUnregisterNotify{
						Message: "machine unregistered, notify from http api",
					},
				}
				jsonBody, err := json.Marshal(notify)
				if err != nil {
					log.Log.WithFields(logrus.Fields{
						"machine": machine,
					}).Errorf("send unregister notify message failed %v", err)
				} else {
					client.WriteResponse(&types.WsResponse{
						WsHeader: types.WsHeader{
							Version:   0,
							Timestamp: time.Now().Unix(),
							Id:        0,
							Type:      uint32(types.WsMtNotify),
							PubKey:    []byte(""),
							Sign:      []byte(""),
						},
						Code:    0,
						Message: "notify",
						Body:    jsonBody,
					})
					log.Log.WithFields(logrus.Fields{
						"machine": machine,
					}).Info("begin to send unregister notify message")
				}
				return false
			}
		}
		return true
	})
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

// checkMissingMachineInfo 扫描当前连接的客户端，对缺少 deeplink_st 硬件信息的机器
// 发送 Type 5 (WsMtRequestMachineInfo) 请求，触发客户端重新发送 Type 2 硬件信息
func (h *Hub) checkMissingMachineInfo(ctx context.Context) {
	// 批量查询所有缺少 deeplink_st 的机器，避免逐个连接查 DB
	incompleteMachines, err := db.MDB.GetMachinesWithoutST(ctx)
	if err != nil {
		log.Log.Errorf("[MachineInfo] Failed to query incomplete machines: %v", err)
		return
	}
	if len(incompleteMachines) == 0 {
		return
	}

	// 构建 machineId → true 的查找表
	incompleteSet := make(map[string]bool, len(incompleteMachines))
	for _, m := range incompleteMachines {
		incompleteSet[m.MachineId] = true
	}

	// 遍历连接客户端，匹配缺失集合
	h.wsConns.Range(func(key, value any) bool {
		client, ok := key.(*Client)
		if !ok || client.MachineKey.MachineId == "" {
			return true
		}
		if incompleteSet[client.MachineKey.MachineId] {
			log.Log.WithFields(logrus.Fields{
				"machine": client.MachineKey,
			}).Info("[MachineInfo] Requesting hardware info (connected but no deeplink_st)")
			client.WriteResponse(&types.WsResponse{
				WsHeader: types.WsHeader{
					Type:      uint32(types.WsMtRequestMachineInfo),
					Timestamp: time.Now().Unix(),
				},
				Code:    0,
				Message: "request machine info",
			})
		}
		return true
	})
}

func (do *delayOffline) Offline(info delayOfflineChanInfo) {
	do.wg.Add(1)
	defer do.wg.Done()

	// Check if this is a FreeRental machine before reporting offline
	if dbc.DbcChain.FreeRentalEnabled() {
		ctx0, cancel0 := context.WithTimeout(context.Background(), 15*time.Second)
		isFreeRental, err := dbc.DbcChain.IsFreeRentalMachine(ctx0, info.machine.MachineId)
		cancel0()
		if err != nil {
			// RPC 失败时跳过惩罚（宁漏报不误罚，FreeRental 机器走 staked path 会 revert 浪费 gas）
			log.Log.WithFields(logrus.Fields{
				"machine": info.machine,
			}).Warnf("IsFreeRentalMachine RPC failed: %v, skipping penalty for safety", err)
			ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel2()
			db.MDB.OfflineMachine(ctx2, info.machine, time.Now())
			do.SendOnlineNotify(info.machine, false, "")
			return
		}
		if isFreeRental {
			do.offlineFreeRental(info)
			return
		}
	}

	do.offlineStaked(info)
}

// offlineFreeRental handles offline for FreeRental machines:
// only penalize if the machine is currently rented.
func (do *delayOffline) offlineFreeRental(info delayOfflineChanInfo) {
	ctx1, cancel1 := context.WithTimeout(context.Background(), 15*time.Second)
	isRented, err := dbc.DbcChain.IsFreeRentalRented(ctx1, info.machine.MachineId)
	cancel1()
	if err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": info.machine,
		}).Warnf("[FreeRental] IsFreeRentalRented RPC failed for %s: %v, skipping penalty (safety: no punishment on RPC failure)", info.machine.MachineId, err)
		// 安全策略：RPC 失败时跳过惩罚，避免误调质押合约 Report
		return
	}

	if !isRented {
		log.Log.WithFields(logrus.Fields{
			"machine": info.machine,
		}).Info("FreeRental machine offline but not rented, skipping penalty")
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()
		db.MDB.OfflineMachine(ctx2, info.machine, time.Now())
		do.SendOnlineNotify(info.machine, false, "")
		return
	}

	// ★ [2026-06-01 防误退租] 同 offlineStaked: 误判机理与机器是质押版/免质押版无关。链上 NotifyFreeRental 退租前先确认机器对主服务是否真在线。
	if do.checkMachineOnlineBeforeReport(info.machine) {
		log.Log.WithField("machine", info.machine.MachineId).Warn(
			"[FreeRental] DDN detected offline but backend confirms ONLINE+SDK ok — skip chain NotifyFreeRental to prevent false eviction")
		ctxg, cancelg := context.WithTimeout(context.Background(), 10*time.Second)
		db.MDB.OfflineMachine(ctxg, info.machine, time.Now())
		cancelg()
		return
	}

	// Machine is rented — call FreeRental.notify(4, machineId) with tp=4 (MachineOffline)
	const maxRetries = 3
	success := false
	for retries := 0; retries < maxRetries; retries++ {
		ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Second)
		hash, err := dbc.DbcChain.NotifyFreeRental(ctx1, 4, info.machine.MachineId)
		cancel1()
		if err != nil {
			log.Log.WithFields(logrus.Fields{
				"machine": info.machine,
			}).Errorf(
				"FreeRental notify offline failed with hash %v because of %v (attempt %d/%d)",
				hash, err, retries+1, maxRetries,
			)
		} else {
			log.Log.WithFields(logrus.Fields{
				"machine": info.machine,
			}).Infof("FreeRental notify offline success with hash %v", hash)
			do.SendOnlineNotify(info.machine, false, hash)
			success = true
			break
		}
	}
	if !success {
		log.Log.WithFields(logrus.Fields{
			"machine": info.machine,
		}).Errorf("[FreeRental] NotifyFreeRental failed after %d retries for %s", maxRetries, info.machine.MachineId)
		do.SendOnlineNotify(info.machine, false, "")
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	db.MDB.OfflineMachine(ctx2, info.machine, time.Now())
}

// checkMachineOnlineBeforeReport 在链上 Report(MachineOffline) 强制退租 *之前*, 向后端探测机器是否实际在线。
// 根因: 检测节点判离线可能是误判 —— 机器对检测节点 WS 失联(客户端连检测节点已知 bug), 但对主服务+SDK 仍在线。
// 原流程"先链上 Report 后才 SendOnlineNotify"使后端 false-offline 闸门来不及拦, 在线机器被误强制退租。
// 本函数用 check_only 模式让后端只做在线判断(device.online + heartbeat<75s + signal_status==0)、零副作用,
// 返回 true = 后端确认在线(误判) → 调用方应跳过链上 Report。
// 保守策略: 后端不可达/非200/解析失败/非 DeepLinkEVM 一律返 false(按原逻辑 Report, 绝不漏真离线惩罚)。
func (do *delayOffline) checkMachineOnlineBeforeReport(machine types.MachineKey) bool {
	if machine.Project != "DeepLinkEVM" {
		return false
	}
	onr := types.OfflineNotifyRequest{
		MachineId: machine.MachineId,
		IsOnline:  false,
		CheckOnly: true,
	}
	jsonData, err := json.Marshal(onr)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 10 * time.Second} // 防后端 hang 永久阻塞 offline goroutine + 卡死优雅关闭(do.wg.Wait); 超时按 unreachable→返 false 照常 Report, 不漏真离线
	resp, err := client.Post(do.notifyApi, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Log.WithField("machine", machine.MachineId).Warnf(
			"checkMachineOnlineBeforeReport: backend unreachable/timeout, proceeding with report (no skip): %v", err)
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		return false
	}
	result := types.OfflineNotifyResponse{}
	if err := json.Unmarshal(body, &result); err != nil {
		return false
	}
	return result.FalseOfflineGuard
}

// offlineStaked handles the original offline flow for staked machines.
func (do *delayOffline) offlineStaked(info delayOfflineChanInfo) {
	// 判断机器是否正在被租赁
	isRented := false
	if dbc.DbcChain.HasRentContract() {
		ctx0, cancel0 := context.WithTimeout(context.Background(), 15*time.Second)
		rented, err := dbc.DbcChain.IsRented(ctx0, info.machine.MachineId)
		cancel0()
		if err == nil {
			isRented = rented
		} else {
			log.Log.WithField("machine", info.machine.MachineId).Warnf(
				"failed to check isRented, skipping penalty (RPC may be down): %v", err)
			// RPC 查询失败时跳过惩罚（宁可漏报也不误罚，误罚造成经济损失不可逆）
			isRented = false
		}
	}

	if isRented {
		// ★ [2026-06-01 防误退租] 链上 Report 会强制退租且不可逆。机器可能只是对检测节点 WS 失联、
		// 对主服务+SDK 仍在线(客户端连检测节点已知 bug)。Report 前先问后端实时在线状态, 确认在线(误判)则跳过 Report。
		if do.checkMachineOnlineBeforeReport(info.machine) {
			log.Log.WithField("machine", info.machine.MachineId).Warn(
				"DDN detected offline but backend confirms ONLINE+SDK ok — skip chain MachineOffline Report to prevent false eviction")
			ctxg, cancelg := context.WithTimeout(context.Background(), 10*time.Second)
			db.MDB.OfflineMachine(ctxg, info.machine, time.Now())
			cancelg()
			return
		}
		// 租赁中离线 → 调链上 Report(MachineOffline) → 触发惩罚 + 退费
		log.Log.WithField("machine", info.machine.MachineId).Info(
			"rented machine offline, reporting MachineOffline for penalty")
		const maxRetries = 3
		retries := 0
		reportSuccess := false
		for retries < maxRetries {
			ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Second)
			hash, err := dbc.DbcChain.Report(
				ctx1,
				types.MachineOffline,
				info.stakingType,
				info.machine.Project,
				info.machine.MachineId,
			)
			cancel1()
			if err != nil {
				log.Log.WithFields(logrus.Fields{
					"machine": info.machine,
				}).Errorf("rented machine offline report failed (hash=%v): %v", hash, err)
				retries++
			} else {
				log.Log.WithFields(logrus.Fields{
					"machine": info.machine,
				}).Info("rented machine offline report success, hash=", hash)
				do.SendOnlineNotify(info.machine, false, hash)
				reportSuccess = true
				break
			}
		}
		// 链上 Report 全失败时仍通知 Node.js 终止租赁并创建 penalty_record（hash 为空）
		if !reportSuccess {
			log.Log.WithField("machine", info.machine.MachineId).Warn(
				"chain report failed after all retries, notifying Node.js to terminate rental anyway")
			do.SendOnlineNotify(info.machine, false, "")
		}
	} else {
		// 纯挖矿离线 → 不调链上惩罚，只标记离线（链上自动停发奖励）
		log.Log.WithFields(logrus.Fields{
			"machine": info.machine,
		}).Info("mining machine offline, skipping chain penalty (rewards will stop automatically)")
		do.SendOnlineNotify(info.machine, false, "")

		// 竞态保护：60 秒后二次确认 isRented，防止"check 时未租但随后被租"的窗口
		go func(machineId, project string, st types.StakingType) {
			time.Sleep(60 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if dbc.DbcChain.HasRentContract() {
				rented, err := dbc.DbcChain.IsRented(ctx, machineId)
				if err == nil && rented {
					log.Log.WithField("machineId", machineId).Warn(
						"race detected: machine became rented after offline, reporting MachineOffline now")
					// ★ [2026-06-01 防误退租] 此延迟退租路径同样绕过 guard, 且踢的是刚租 60s 的新租约(体验最差)。Report 前先确认机器对主服务是否真在线。
					delayedMachineGuard := types.MachineKey{MachineId: machineId, Project: project}
					if do.checkMachineOnlineBeforeReport(delayedMachineGuard) {
						log.Log.WithField("machineId", machineId).Warn(
							"delayed report: backend confirms ONLINE+SDK ok — skip chain MachineOffline Report to prevent false eviction")
						return
					}
					ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel2()
					if hash, err := dbc.DbcChain.Report(ctx2, types.MachineOffline, st, project, machineId); err != nil {
						log.Log.WithField("machineId", machineId).Errorf("delayed rental offline report failed: %v", err)
					} else {
						log.Log.WithField("machineId", machineId).Infof("delayed rental offline report success, hash=%s", hash)
						// 补发通知，让 Node.js 创建带 tx hash 的 penalty_record
						delayedMachine := types.MachineKey{MachineId: machineId, Project: project}
						do.SendOnlineNotify(delayedMachine, false, hash)
					}
				}
			}
		}(info.machine.MachineId, info.machine.Project, info.stakingType)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	db.MDB.OfflineMachine(ctx2, info.machine, time.Now())
}

func (do *delayOffline) SendOnlineNotify(machine types.MachineKey, isOnline bool, reportTxHash string) {
	onr := types.OfflineNotifyRequest{
		MachineId: machine.MachineId,
		IsOnline:      isOnline,
		ReportTxHash: reportTxHash,
	}
	jsonData, err := json.Marshal(onr)
	if err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
			"online":  isOnline,
		}).Errorf("failed to send online notify request: %v", err)
	}

	const maxRetries = 3
	retries := 0
	for retries < maxRetries && machine.Project == "DeepLinkEVM" {
		resp, err := http.Post(
			do.notifyApi,
			"application/json",
			bytes.NewBuffer(jsonData),
		)
		if err != nil {
			log.Log.WithFields(logrus.Fields{
				"machine": machine,
				"online":  isOnline,
			}).Errorf("failed to send online notify request %v times: %v", retries, err)
			retries++
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() // 立即关闭，不用 defer（在循环内 defer 会泄漏资源）
		if err != nil || resp.StatusCode != 200 {
			log.Log.WithFields(logrus.Fields{
				"machine": machine,
				"online":  isOnline,
			}).Errorf("failed to send online notify request %v times: status=%d err=%v", retries, resp.StatusCode, err)
			retries++
			continue
		}
		result := types.OfflineNotifyResponse{}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Log.WithFields(logrus.Fields{
				"machine": machine,
				"online":  isOnline,
			}).Errorf("failed to parse online notify response %v times: %v", retries, err)
			retries++
			continue
		}
		if result.Code == 1 {
			log.Log.WithFields(logrus.Fields{
				"machine": machine,
				"online":  isOnline,
			}).Infof("send online notify request success %v %v", result.Success, result.Msg)
			break
		}
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
			"online":  isOnline,
		}).Errorf("online notify request rejected %v times: code=%v msg=%v", retries, result.Code, result.Msg)
		retries++
	}
}
