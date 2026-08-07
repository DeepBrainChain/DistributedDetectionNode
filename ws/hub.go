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

	// [FIX 2026-07-28 B] machineOwner = 每台机器"当前所有者连接"的内存登记，ownerMu 保护。
	// 上线时原子接管(becomeOwner)：把该机器的所有者切成新连接、返回旧所有者以便顶替(关闭+标 superseded)。
	// 退出时按 releaseOwner 判定本连接是否仍是所有者——只有仍是所有者才删记录/排下线，
	// 被顶替的旧/在途连接退出绝不动新所有者的记录。解决"在途连接晚到覆盖并删掉活机器记录→误下线活机"。
	ownerMu      sync.Mutex
	machineOwner map[types.MachineKey]*Client
}

type cachedOfflineItem struct {
	disconnectTime time.Time
	stakingType    types.StakingType
	// [2026-08-07 抖动逃罚上限] 本机第一次进入「宽限推迟」循环的时刻（rearmForRecheck 起算）。
	//   持续抖动/离线的租用机每次 recentGrace 都重置 disconnectTime=now → 5min 时钟永远续、永不判罚
	//   （逃罚 + 租客不退款）。用它给「累计推迟时长」加时间上限：超上限就停止宽限、照常判罚。
	//   机器真重连(do.connect 从 elements 删除)会自然清零 → 下次掉线重新起算，只惩罚「连续 N 分钟从没稳定连上」的机器。
	//   零值 = 尚未进入宽限循环（正常首次判定）。
	firstDeferredAt time.Time
}

type delayOfflineChanInfo struct {
	machine        types.MachineKey
	disconnectTime time.Time
	stakingType    types.StakingType
	// [2026-08-07 抖动逃罚上限] 随 diconnect/ticker 传递首次推迟时刻，见 cachedOfflineItem.firstDeferredAt。
	firstDeferredAt time.Time
}

// [2026-08-07 抖动逃罚上限] 累计推迟(宽限)时长的时间上限。某台租用机连续推迟超过它 = 持续抖动/离线、
//   从没稳定连上 → 停止宽限、照常判罚（防「无限宽限=逃罚+租客不退款」）。取 30min：远大于正常重启恢复
//   (含无盘慢启动 ~9.5min)、又能在合理时间内对持续抖动落罚。★ 用「累计推迟时长」而非「重排次数」：
//   次数上限会把「先抖后死」的机器提前放弃推给 cron 兜底(会漏)，时间上限则一路推迟到期限再判、不漏。
var maxDeferWindow = 30 * time.Minute

type delayOffline struct {
	hub       *Hub // [FIX 2026-07-28 B] 回指 Hub，供下线复核直接查"内存所有权(是否有活连接)"而非 DB 记录
	connect   chan delayOfflineChanInfo
	diconnect chan delayOfflineChanInfo
	elements  map[types.MachineKey]cachedOfflineItem
	wg        sync.WaitGroup
	done      chan bool
	// stopped 在 HandleDelayOffline 退出时 close，作为广播停止信号。
	// 生产者（readPump/handleOnlineRequest 向 connect/diconnect 发送、延迟退租 goroutine 的 sleep）select 它以避免
	// 向已关闭 channel 发送 panic 及关闭时永久阻塞/慢退出。替代旧的"接收方 close connect/diconnect"反模式。
	stopped   chan struct{}
	notifyApi string
}

func InitHub(ctx context.Context, napi string) (*Hub, error) {
	// [僵尸连接根治] 启动时清空 machine_connection 孤儿记录。此刻进程刚起、尚无任何活 WS 连接,
	// 集合里残留的都是上一个进程周期 (重启/崩溃/被 kill) 未经断开清理遗留的孤儿。不清理会让机器重连被
	// IsMachineConnected 永久判 "repeated connection" 拒绝 → 上不了链上在线 → 不可租。详见
	// db.ClearAllMachineConnections 注释。清理失败不阻断启动 (仅告警), 避免 Mongo 抖动时 DDN 起不来。
	{
		clearCtx, clearCancel := context.WithTimeout(ctx, 30*time.Second)
		if n, err := db.MDB.ClearAllMachineConnections(clearCtx); err != nil {
			log.Log.Errorf("[startup] clear stale machine_connection records failed (continuing): %v", err)
		} else {
			log.Log.Infof("[startup] cleared %d stale machine_connection records (orphans from previous process cycle)", n)
		}
		clearCancel()
	}

	hub := &Hub{
		wg:           sync.WaitGroup{},
		wsConns:      sync.Map{},
		machineOwner: make(map[types.MachineKey]*Client),
		do: &delayOffline{
			connect:   make(chan delayOfflineChanInfo),
			diconnect: make(chan delayOfflineChanInfo),
			elements:  make(map[types.MachineKey]cachedOfflineItem),
			wg:        sync.WaitGroup{},
			done:      make(chan bool),
			stopped:   make(chan struct{}),
			notifyApi: napi,
		},
	}
	hub.do.hub = hub // [FIX 2026-07-28 B] 回指，供下线复核查内存所有权
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
	go hub.runOrphanConnReaper(ctx) // [FIX 2026-07-28 B] 周期清理无活连接所有者的孤儿 machine_connection 记录
	// [FIX 2026-07-28 B] ★单实例硬约束★：machineOwner 内存所有权 + 孤儿清理 + delayOffline 下线复核
	// 都基于「本进程内存」，只在 DDN 单实例(当前 47.130.164.32)下正确。严禁未加 node 归属前横向扩容多实例——
	// 否则一节点会把另一节点持有的活机器误判离线→误上链下线/惩罚/退租(不可逆资金动作)。启动即告警提醒运维。
	log.Log.Warn("[DDN single-instance assumption] offline decisions use in-process ownership (machineOwner) + orphan reaper; SAFE ONLY as a single DDN instance. Do NOT run multiple instances against the same MongoDB without adding node-ownership, or a node will wrongly report on-chain OFFLINE for machines connected to another node.")
	hub.open.Store(true)
	return hub, nil
}

// runOrphanConnReaper 周期性清理 machine_connection 里"已无活连接所有者"的孤儿记录。
// [FIX 2026-07-28 B] 兜底:MachineDisconnected 删除失败(DB 抖动)、或并发上线残留的记录若没人清，
// 会永久让 HandleDelayOffline 的 IsMachineConnected 复核误判"机器仍在线"→压制掉线机器的下线/退租/惩罚
// (money 漏)。集合无 TTL,故用这个基于"内存所有权登记"的周期对账来收割孤儿(跨机型/掉电 crash 都覆盖)。
func (h *Hub) runOrphanConnReaper(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if h.closed() {
				return
			}
			h.reapOrphanConnections(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// reapOrphanConnections 清理"已无活连接所有者"的孤儿 machine_connection 记录。
// ⚠️ 假设 DDN 单实例部署(当前即是,47.130.164.32)：owned 是本进程内存所有权，若将来多实例共享
//
//	machine_connection，本节点会把"别节点持有的活记录"误当孤儿——多实例需另加节点归属(node_id)后再放开。
//
// 安全点：按【记录自己的 conn_id 精确删】，即使"删除判定 → 实际删除"之间机器重连覆盖了记录，
//
//	新连接写的是不同 conn_id → filter 不匹配 → 绝不误删刚重连的活记录(修上一版按 MachineKey 删的 TOCTOU)。
func (h *Hub) reapOrphanConnections(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	recs, err := db.MDB.AllConnectionRecords(rctx)
	if err != nil {
		log.Log.Warnf("orphan reaper: list machine_connection failed: %v", err)
		return
	}
	owned := h.ownedMachines()
	for _, rec := range recs {
		if rec.ConnId == "" {
			// [FIX 2026-07-28 B] 无 conn_id 记录一律不走"按 MachineKey 删"路径(那会有重连 TOCTOU)。
			// 本版写入都带 conn_id、启动已清老版本遗留的无 conn_id 记录 → 正常不会遇到；万一遇到只告警,
			// 交给启动清理/手动处理,绝不在此按 MachineKey 删。
			log.Log.Warnf("orphan reaper: unexpected machine_connection record without conn_id (machine=%v); skipping (not deleting by MachineKey)", rec.MachineKey)
			continue
		}
		if _, ok := owned[rec.MachineKey]; ok {
			continue // 有活连接所有者 → 保留(含本记录 conn_id 就是当前所有者的情形)
		}
		// 该机器无任何本节点活连接所有者 → 这条记录是孤儿(所有者已走但删除失败残留/掉电 crash 未清)。
		// 按 (MachineKey, 本记录 conn_id) 精确删：只删这条,不碰同机器可能已被新连接写的其它 conn_id 记录。
		if derr := db.MDB.MachineDisconnected(rctx, rec.MachineKey, rec.ConnId); derr != nil {
			log.Log.Warnf("orphan reaper: delete orphan %v(conn=%s) failed: %v", rec.MachineKey, rec.ConnId, derr)
		} else {
			log.Log.WithFields(logrus.Fields{"machine": rec.MachineKey, "connId": rec.ConnId}).Info("orphan reaper: removed orphan machine_connection record (no live owner)")
		}
	}
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

// hasLiveConnection 返回本 DDN 实例当前是否持有该机器的一条活 WS 连接
// (wsConns 里存在一个 MachineKey 相等的 *Client)。
//
// 用于 handleOnlineRequest 区分两种"machine_connection 记录已存在"的情形:
//   - 本实例确有活连接 → 真·重复连接 → 应拒;
//   - 本实例无活连接 → 该记录是孤儿 (上个进程周期被 kill 遗留 / 本周期非正常断开且 DeleteOne
//     失败遗留) → 应清掉并放行, 让机器重连成功, 不必等下次 DDN 重启。
//
// 只查本实例内存 wsConns → 天然多实例安全 (各实例只认自己的活连接集, 不会误判/误清对端实例的连接)。
// 注意: 正在握手、尚未 online 成功的连接其 MachineKey 为空 (line ~410 才赋值), 不会被匹配,
// 故发起本次 online 请求的连接自身绝不会把自己算成"已有活连接"。
func (h *Hub) hasLiveConnection(mk types.MachineKey) bool {
	live := false
	h.wsConns.Range(func(key, value any) bool {
		if client, ok := key.(*Client); ok {
			if client.MachineKey == mk {
				live = true
				return false // 命中即停止遍历
			}
		}
		return true
	})
	return live
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

// becomeOwner 原子地把 machine 的当前所有者切成 c，返回被顶替的旧所有者(若有)并把它关闭+标 superseded。
// [FIX 2026-07-28 B] 用内存所有权登记取代旧的 closeExistingConnection(它按 MachineKey 扫 wsConns、
// 看不见"MachineKey 还没赋值的在途上线连接"，且在校验前就关旧连接)。becomeOwner 只切内存登记(不做 DB、
// 不持锁做慢操作)，由调用方在"校验全过、准备写记录"时调用：
//
//	① prev(旧所有者)被标 superseded + 关闭 → 其 readPump 退出后 releaseOwner 判定自己已非所有者、不动记录;
//	② 新连接随后 MachineConnected 写自己的 conn_id;
//	③ 若本连接在写记录前又被更新连接顶替(自身 superseded=true)，调用方放弃写、不覆盖新所有者记录。
func (h *Hub) becomeOwner(machine types.MachineKey, c *Client) (prev *Client) {
	h.ownerMu.Lock()
	prev = h.machineOwner[machine]
	h.machineOwner[machine] = c
	h.ownerMu.Unlock()
	if prev != nil && prev != c {
		prev.superseded.Store(true)
		log.Log.WithFields(logrus.Fields{
			"machine":   machine,
			"oldConnId": prev.ClientID,
			"newConnId": c.ClientID,
		}).Info("machine ownership taken over by new connection (replace, not reject)")
		prev.conn.Close() // 触发旧所有者 readPump 退出；其清理经 releaseOwner 判定已非所有者→不删记录/不排下线
	}
	return prev
}

// releaseOwner 连接退出时调用：仅当 c 仍是 machine 的当前所有者才移除登记并返回 true。
// 被顶替的旧/在途连接返回 false → 调用方不删 machine_connection 记录、不排延迟下线，
// 从而绝不会删掉/顶替当前活所有者的记录(修"误删活机记录→误下线活机")。
func (h *Hub) releaseOwner(machine types.MachineKey, c *Client) bool {
	h.ownerMu.Lock()
	defer h.ownerMu.Unlock()
	if h.machineOwner[machine] == c {
		delete(h.machineOwner, machine)
		return true
	}
	return false
}

// ownedMachines 返回当前有活所有者的机器集合(供孤儿清理判定哪些 machine_connection 记录已无活连接)。
// isMachineOwned 报告该机器当前是否有活连接所有者(内存判定,ownerMu 下)。[FIX 2026-07-28 B]
// 用于下线复核:有活连接=在线→跳过下线;无=离线→触发下线(不看可能残留的 DB 记录)。
func (h *Hub) isMachineOwned(machine types.MachineKey) bool {
	h.ownerMu.Lock()
	defer h.ownerMu.Unlock()
	return h.machineOwner[machine] != nil
}

func (h *Hub) ownedMachines() map[types.MachineKey]struct{} {
	h.ownerMu.Lock()
	defer h.ownerMu.Unlock()
	out := make(map[types.MachineKey]struct{}, len(h.machineOwner))
	for mk := range h.machineOwner {
		out[mk] = struct{}{}
	}
	return out
}

func (do *delayOffline) HandleDelayOffline() {
	ticker := time.NewTicker(10 * time.Second)
	defer func() {
		ticker.Stop()
		// 广播停止：不再 close connect/diconnect（接收方关闭被多生产者写的 channel 是反模式，会 send-on-closed panic）。
		// 生产者改 select <-do.stopped 退出。
		close(do.stopped)
	}()
	for {
		select {
		case item := <-do.connect:
			delete(do.elements, item.machine)
		case item := <-do.diconnect:
			do.elements[item.machine] = cachedOfflineItem{
				disconnectTime:  item.disconnectTime,
				stakingType:     item.stakingType,
				firstDeferredAt: item.firstDeferredAt, // 首次推迟时刻(fresh 掉线为零值; rearm 时携带累计起点)
			}
		case <-ticker.C:
			expired := time.Now().Add(-5 * time.Minute)
			for machine, cdi := range do.elements {
				if cdi.disconnectTime.Before(expired) {
					// [FIX 2026-07-28 B] 下线真正触发前复核"该机器当前是否有活连接(内存所有权)"：有→宽限期内它(或替换
					//   连接)重连了→跳过下线(修「替换旧连接的 diconnect 排了下线、而新连接 connect 因 channel 随机顺序
					//   没取消」的竞态,防误下线在线/挖矿/被租机器)。★ 用内存所有权 isMachineOwned 而非 DB 记录判定：
					//   ①DDN 单实例、内存所有权即"有无活连接"的权威；②避免陈旧/错 conn_id 残留记录误让机器显得在线
					//   →压制正当下线(死机漏惩罚)。isMachineOwned 只读内存(ownerMu 下)不阻塞、不依赖 DB。
					if do.hub != nil && do.hub.isMachineOwned(machine) {
						delete(do.elements, machine) // 有活连接→取消本次下线(等它真断线再排)
						continue
					}
					do.wg.Add(1)
					go func(coi delayOfflineChanInfo) {
						defer do.wg.Done()
						do.Offline(coi)
					}(delayOfflineChanInfo{
						machine:         machine,
						disconnectTime:  cdi.disconnectTime,
						stakingType:     cdi.stakingType,
						firstDeferredAt: cdi.firstDeferredAt, // 传给 Offline→rearmForRecheck 判累计推迟上限
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

// Offline 由 HandleDelayOffline 通过 `go` 派生调用，wg.Add/Done 由派生处管理（见 HandleDelayOffline ticker 分支）。
func (do *delayOffline) Offline(info delayOfflineChanInfo) {
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
	if skipReport, recentGrace := do.checkMachineOnlineBeforeReport(info.machine); skipReport {
		if recentGrace {
			// 刚离线(<5min, 大概率重启) → 推迟重判, 不放弃(FreeRental 同样覆盖, 防真死机不下架)
			if do.rearmForRecheck(info) {
				log.Log.WithField("machine", info.machine.MachineId).Warn(
					"[FreeRental] backend: recently offline (<5min, likely reboot) — DEFER, re-arm for re-check in ~5min")
				return
			}
			// [抖动逃罚上限] 累计推迟超上限(持续抖动、从没稳定连上) → 不再宽限，落到下面照常判罚(不 return)
			log.Log.WithField("machine", info.machine.MachineId).Warn(
				"[FreeRental] deferred too long (persistent flapping) — proceeding to penalty despite recent-offline grace")
		} else {
			log.Log.WithField("machine", info.machine.MachineId).Warn(
				"[FreeRental] DDN detected offline but backend confirms ONLINE+SDK ok — skip chain NotifyFreeRental to prevent false eviction")
			ctxg, cancelg := context.WithTimeout(context.Background(), 10*time.Second)
			db.MDB.OfflineMachine(ctxg, info.machine, time.Now())
			cancelg()
			return
		}
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
// checkMachineOnlineBeforeReport 返回 (skipReport, recentGrace):
//   skipReport=true  → 本次不发链上 Report(防误退租)。
//   recentGrace=true → 原因是「机器刚离线不久(<5min, 大概率重启)」,调用方应**推迟重判**(rearmForRecheck)而非放弃;
//   recentGrace=false 而 skipReport=true → 原因是「机器其实在线(误判)」,调用方**放弃**(原行为, 无需重判)。
func (do *delayOffline) checkMachineOnlineBeforeReport(machine types.MachineKey) (bool, bool) {
	if machine.Project != "DeepLinkEVM" {
		return false, false
	}
	onr := types.OfflineNotifyRequest{
		MachineId: machine.MachineId,
		IsOnline:  false,
		CheckOnly: true,
	}
	jsonData, err := json.Marshal(onr)
	if err != nil {
		return false, false
	}
	client := &http.Client{Timeout: 10 * time.Second} // 防后端 hang 永久阻塞 offline goroutine + 卡死优雅关闭(do.wg.Wait); 超时按 unreachable→返 false 照常 Report, 不漏真离线
	resp, err := client.Post(do.notifyApi, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Log.WithField("machine", machine.MachineId).Warnf(
			"checkMachineOnlineBeforeReport: backend unreachable/timeout, proceeding with report (no skip): %v", err)
		return false, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		return false, false
	}
	result := types.OfflineNotifyResponse{}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, false
	}
	return result.FalseOfflineGuard, result.RecentOfflineGrace
}

// rearmForRecheck: 后端判「刚离线(<5min, 重启)」→ 不放弃, 重置计时重入 do.elements, ~5min 后由 HandleDelayOffline 重判。
//   届时机器若已回来(重启完成)会先被 do.connect 从 elements 删掉→不判罚; 若仍离线(真死机)则 offset≥5min、
//   后端不再宽限→照常 Report slash。这样宽限=「推迟+重判」而非「放弃」, 既不误罚重启又不放过真死机。
// [2026-08-07 抖动逃罚上限] 返回值语义：
//   true  = 已重排（推迟成功），调用方应 return（本轮不判罚）。
//   false = 累计推迟已超 maxDeferWindow（持续抖动/离线），**不再重排**；调用方应**继续往下判罚**（别 return）。
func (do *delayOffline) rearmForRecheck(info delayOfflineChanInfo) bool {
	first := info.firstDeferredAt
	if first.IsZero() {
		first = time.Now() // 第一次进入宽限推迟循环，起算累计
	} else if time.Since(first) > maxDeferWindow {
		log.Log.WithField("machine", info.machine.MachineId).Warnf(
			"[offline] deferred %v > ceiling %v (persistent flapping/offline, never stabilized) — stop deferring, proceed to penalty",
			time.Since(first).Truncate(time.Second), maxDeferWindow)
		return false
	}
	select {
	case do.diconnect <- delayOfflineChanInfo{
		machine:         info.machine,
		disconnectTime:  time.Now(), // 重置计时: 5min 后重判
		stakingType:     info.stakingType,
		firstDeferredAt: first, // 保留首次推迟时刻，累计推迟时长随重排一路传递
	}:
	case <-do.stopped:
	}
	return true
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
		if skipReport, recentGrace := do.checkMachineOnlineBeforeReport(info.machine); skipReport {
			if recentGrace {
				// 刚离线(<5min, 大概率重启) → 推迟重判, 不放弃(防真死机逃罚)
				if do.rearmForRecheck(info) {
					log.Log.WithField("machine", info.machine.MachineId).Warn(
						"backend: recently offline (<5min, likely reboot) — DEFER penalty, re-arm for re-check in ~5min (won't abandon a genuinely dead machine)")
					return
				}
				// [抖动逃罚上限] 累计推迟超上限(持续抖动、从没稳定连上) → 不再宽限，落到下面照常判罚(不 return)
				log.Log.WithField("machine", info.machine.MachineId).Warn(
					"deferred too long (persistent flapping) — proceeding to MachineOffline penalty despite recent-offline grace")
			} else {
				log.Log.WithField("machine", info.machine.MachineId).Warn(
					"DDN detected offline but backend confirms ONLINE+SDK ok — skip chain MachineOffline Report to prevent false eviction")
				ctxg, cancelg := context.WithTimeout(context.Background(), 10*time.Second)
				db.MDB.OfflineMachine(ctxg, info.machine, time.Now())
				cancelg()
				return
			}
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
		// 纳入 do.wg 让优雅关闭等待它；sleep 可被 stopped 取消，避免关闭时干等 60s
		do.wg.Add(1)
		go func(machineId, project string, st types.StakingType) {
			defer do.wg.Done()
			select {
			case <-time.After(60 * time.Second):
			case <-do.stopped:
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if dbc.DbcChain.HasRentContract() {
				rented, err := dbc.DbcChain.IsRented(ctx, machineId)
				if err == nil && rented {
					log.Log.WithField("machineId", machineId).Warn(
						"race detected: machine became rented after offline, reporting MachineOffline now")
					// ★ [2026-06-01 防误退租] 此延迟退租路径同样绕过 guard, 且踢的是刚租 60s 的新租约(体验最差)。Report 前先确认机器对主服务是否真在线。
					delayedMachineGuard := types.MachineKey{MachineId: machineId, Project: project}
					if skipReport, recentGrace := do.checkMachineOnlineBeforeReport(delayedMachineGuard); skipReport {
						if recentGrace {
							log.Log.WithField("machineId", machineId).Warn(
								"delayed: recently offline (<5min, likely reboot) — DEFER, re-arm for re-check in ~5min")
							do.rearmForRecheck(delayOfflineChanInfo{machine: delayedMachineGuard, stakingType: st})
							return
						}
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
		MachineId:    machine.MachineId,
		IsOnline:     isOnline,
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
	// 带超时的 client：防后端 half-open/僵死时 http.Post(默认无超时) 永久阻塞此 goroutine → goroutine 泄漏 + 卡死优雅关闭 do.wg.Wait()
	notifyClient := &http.Client{Timeout: 10 * time.Second}
	for retries < maxRetries && machine.Project == "DeepLinkEVM" {
		resp, err := notifyClient.Post(
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
