package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/mongo"

	"DistributedDetectionNode/db"
	"DistributedDetectionNode/dbc"
	"DistributedDetectionNode/dbc/calculator"
	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 30 * time.Second // 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		if r.Method != "GET" {
			return false
		}
		if r.URL.Path != "/echo" && r.URL.Path != "/websocket" {
			return false
		}
		return true
	},
} // use default options

type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send     chan envelope
	sendDone chan bool

	MachineKey  types.MachineKey
	StakingType types.StakingType
	ClientIP    string
	ClientID    string

	// [FIX 2026-07-28 B] superseded 在本连接被同机器的更新连接顶替(becomeOwner 时)置 true。
	// 用途:被顶替的"在途上线"连接在写 machine_connection 记录前发现自己已被顶替→放弃写(不覆盖
	// 新所有者的记录);退出清理也据"是否仍是当前所有者"决定删不删记录/排不排下线。
	superseded atomic.Bool
}

type envelope struct {
	t   int
	msg []byte
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump(ctx context.Context) {
	log.Log.WithFields(logrus.Fields{
		"uuid":    c.ClientID,
		"machine": c.MachineKey,
	}).Info("read goroutine started")
	// c.hub.wg.Add(1)
	defer func() {
		// c.hub.wg.Done()
		c.hub.wsConns.Delete(c)
		// 只 close sendDone 作停止信号，不 close c.send：
		// c.send 有多个发送方（writePump 之外的 SendUnregisterNotify/checkMissingMachineInfo 经 WriteEnvelope），
		// 由 reader 关闭会导致 send-on-closed panic。writePump 改听 sendDone 退出，WriteEnvelope select sendDone 防阻塞。
		close(c.sendDone)
		c.conn.Close()

		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).Info("read goroutine stopped")
	}()
	// c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	// c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	c.conn.SetPingHandler(func(appData string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).Debug("ping handler")
		err := c.conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(writeWait))
		if err == websocket.ErrCloseSent {
			return nil
		} else if e, ok := err.(net.Error); ok && e.Temporary() {
			return nil
		}
		return err
	})
	for {
		mt, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Log.WithFields(logrus.Fields{
					"uuid":    c.ClientID,
					"machine": c.MachineKey,
				}).Error("read error: ", err)
			}
			break
		}
		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).Infof("recv message: %v %s", mt, message)

		// message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		// c.hub.broadcast <- message
		req := &types.WsRequest{}
		if err := json.Unmarshal(message, req); err != nil {
			log.Log.WithFields(logrus.Fields{
				"uuid":    c.ClientID,
				"machine": c.MachineKey,
			}).Error("parse request failed: ", err)
			c.WriteResponse(&types.WsResponse{
				WsHeader: types.WsHeader{
					Version:   0,
					Timestamp: time.Now().Unix(),
					Id:        0,
					Type:      0,
					PubKey:    []byte(""),
					Sign:      []byte(""),
				},
				Code:    uint32(types.ErrCodeParam),
				Message: "parse request failed",
				Body:    []byte(""),
			})
			continue
		}

		// go c.handleRequest(ctx, req)
		c.handleRequest(ctx, req)
	}

	if c.MachineKey.MachineId != "" {
		// [FIX 2026-07-28 B] 所有权感知清理：仅当本连接【仍是该机器的当前所有者】才删记录+排延迟下线。
		//   releaseOwner 原子判定并释放：若本连接已被更新连接顶替(superseded)，返回 false → 本连接
		//   【绝不删记录、绝不排下线】——因为记录/下线归属现任所有者，被顶替的旧/在途连接动它就会
		//   误删活机器记录、误触发对活机器的下线(上一版的 HIGH bug)。conn_id 感知删除是第二道保险。
		if c.hub.releaseOwner(c.MachineKey, c) {
			db.MDB.MachineDisconnected(ctx, c.MachineKey, c.ClientID)
			mi, err := db.MDB.GetMachineInfo(ctx, c.MachineKey)
			if err == nil && mi.CalcPoint != 0 && !c.hub.closed() {
				// select stopped 防 HandleDelayOffline 已退出后向无接收方 channel 发送而阻塞/panic
				select {
				case c.hub.do.diconnect <- delayOfflineChanInfo{
					machine:        c.MachineKey,
					disconnectTime: time.Now(),
					stakingType:    c.StakingType,
				}:
				case <-c.hub.do.stopped:
				}
			}
		} else {
			log.Log.WithFields(logrus.Fields{
				"machine": c.MachineKey,
				"connId":  c.ClientID,
			}).Info("connection was superseded by a newer one; skip disconnect cleanup (not current owner)")
		}
		// pm.DeleteMetrics(machine)
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump(ctx context.Context) {
	log.Log.WithFields(logrus.Fields{
		"uuid":    c.ClientID,
		"machine": c.MachineKey,
	}).Info("write goroutine started")
	c.hub.wg.Add(1)
	defer func() {
		c.hub.wg.Done()
		c.conn.Close()

		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).Info("write goroutine stopped")
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if message.t == websocket.CloseMessage {
				c.conn.WriteMessage(websocket.CloseMessage, message.msg)
				return
			}

			w, err := c.conn.NextWriter(message.t) //websocket.TextMessage
			if err != nil {
				return
			}
			w.Write(message.msg)

			// Add queued chat messages to the current websocket message.
			// n := len(c.send)
			// for i := 0; i < n; i++ {
			// 	w.Write(newline)
			// 	w.Write(<-c.send)
			// }

			if err := w.Close(); err != nil {
				return
			}
		case <-c.sendDone:
			// readPump 已退出（连接断开）→ writePump 收尾退出。替代旧的"靠 c.send 被 close 触发 !ok"机制。
			c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		case <-ctx.Done():
			c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}
}

func (c *Client) WriteResponse(res *types.WsResponse) error {
	resBytes, err := json.Marshal(res)
	if err != nil {
		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).Errorf("marshal reponse with id %v failed: %v", res.Id, err)
		return err
	}

	c.WriteEnvelope(envelope{t: websocket.TextMessage, msg: resBytes})

	return nil
}

func (c *Client) WriteEnvelope(message envelope) {
	// sendDone 关闭后丢弃消息；否则发送。c.send 不再被 close（见 readPump defer 注释），故无 send-on-closed panic；
	// 两 case 同时 ready 时 select 随机选，sendDone 分支保证不会阻塞已退出的 writePump。
	select {
	case <-c.sendDone:
		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).Info("Sender received done signal, dropping message")
	case c.send <- message:
	}
}

func (c *Client) RespondRequest(req *types.WsRequest, code uint32, message string, body []byte) error {
	return c.WriteResponse(&types.WsResponse{
		WsHeader: types.WsHeader{
			Version:   0,
			Timestamp: time.Now().Unix(),
			Id:        req.Id,
			Type:      req.Type,
			PubKey:    []byte(""),
			Sign:      []byte(""),
		},
		Code:    code,
		Message: message,
		Body:    body,
	})
}

func (c *Client) handleRequest(ctx context.Context, req *types.WsRequest) {
	var (
		code    uint32
		message string
		body    []byte
	)
	switch req.Type {
	case uint32(types.WsMtOnline):
		code, message, body = c.handleOnlineRequest(ctx, req)
	case uint32(types.WsMtDeepLinkMachineInfoST):
		code, message, body = c.handleDeepLinkMachineInfoSTRequest(ctx, req)
	case uint32(types.WsMtDeepLinkMachineInfoBW):
		code, message, body = c.handleDeepLinkMachineInfoBandwidthRequest(ctx, req)
	default:
		code = uint32(types.ErrCodeParam)
		message = "unknowned request message type"
		body = []byte("")
	}
	if code == 0 {
		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).Info(message)
	} else {
		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).Error(message)
	}
	c.RespondRequest(req, code, message, body)
}

func (c *Client) handleOnlineRequest(ctx context.Context, req *types.WsRequest) (uint32, string, []byte) {
	if c.MachineKey.MachineId != "" {
		return uint32(types.ErrCodeOnline), "machine has been online, repeated requests", []byte("")
	}

	onlineReq := &types.WsOnlineRequest{}
	if err := json.Unmarshal(req.Body, onlineReq); err != nil {
		return uint32(types.ErrCodeParam), fmt.Sprintf("parse online request failed: %v", err), []byte("")
	}

	if onlineReq.MachineId == "" {
		return uint32(types.ErrCodeParam), "machine id is empty", []byte("")
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// [FIX 2026-07-28 B] 原来这里 IsMachineConnected=true 直接拒绝 "repeated connection" → 机器硬重启后
	//   旧记录残留(读超时 pongWait=30s 才删、集合无 TTL)→重连被锁死。改为"替换"。★ 但接管旧连接+写记录
	//   必须放到"校验全过(GetMachineState/注册/上链 Report)之后"、不能在这里先关旧连接(否则新连接校验失败
	//   会白白把旧连接踢掉、机器掉出 DDN)。所以这里不再动连接，只在下方 MachineConnected 前做 becomeOwner。

	isOnline, isRegistered, err := dbc.DbcChain.GetMachineState(
		ctx,
		onlineReq.Project,
		onlineReq.MachineId,
		onlineReq.StakingType,
	)
	if err != nil {
		return uint32(types.ErrCodeOnline), fmt.Sprintf("failed to get machine state %v", err), []byte("")
	}
	if !isRegistered {
		// blocked 机器 getMachineState=false，但如果正在被租赁，仍允许连接 DDN
		// 这样 DDN 才能监测到租赁中机器离线并触发惩罚
		allowRented := false
		if dbc.DbcChain.HasRentContract() {
			rented, rentErr := dbc.DbcChain.IsRented(ctx, onlineReq.MachineId)
			if rentErr == nil && rented {
				log.Log.WithField("machineId", onlineReq.MachineId).Info(
					"machine not registered but isRented=true, allowing DDN connection for rental offline detection")
				allowRented = true
			}
		}
		if !allowRented {
			return uint32(types.ErrCodeOnline), "machine not registered", []byte("")
		}
	}

	if _, err := db.MDB.GetMachineInfo(ctx, onlineReq.MachineKey); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			if err := db.MDB.RegisterMachine(ctx, onlineReq.MachineKey, onlineReq.StakingType); err != nil {
				return uint32(types.ErrCodeOnline), "failed to register machine in mongo", []byte("")
			}
		} else {
			return uint32(types.ErrCodeOnline), "failed to get registered machine info", []byte("")
		}
	}

	// 对于 isRegistered=false 但 isRented=true 的 blocked 机器，
	// 不报 MachineOnline（防止恢复 calcPoint 导致 blocked 机器重新获得挖矿奖励）
	// 这些机器连接 DDN 仅用于离线监测，不参与挖矿
	if !isOnline && isRegistered {
		if hash, err := dbc.DbcChain.Report(
			ctx,
			types.MachineOnline,
			onlineReq.StakingType,
			onlineReq.Project,
			onlineReq.MachineId,
		); err != nil {
			errMsg := fmt.Sprintf(
				"machine online in chain contract failed with hash %v because of %v",
				hash,
				err,
			)
			return uint32(types.ErrCodeDbcChain), errMsg, []byte("")
		} else {
			log.Log.WithFields(logrus.Fields{
				"uuid":    c.ClientID,
				"machine": onlineReq,
			}).Info("machine online in chain contract success with hash ", hash)
			go c.hub.do.SendOnlineNotify(onlineReq.MachineKey, true, "")
		}
	}

	// [FIX 2026-07-28 B] 先设 MachineKey/StakingType，使万一本连接此后被顶替/断开，其 readPump 清理
	// 能经 releaseOwner(c.MachineKey, c) 正确释放所有权，并按"是否仍是所有者"决定删不删记录。
	c.MachineKey = onlineReq.MachineKey
	c.StakingType = onlineReq.StakingType

	// [FIX 2026-07-28 B] 原子接管该机器所有权：becomeOwner 把所有者切成本连接，并关闭+标 superseded 旧所有者。
	// 放在此处(GetMachineState/注册/上链 Report 全过之后)而非请求开头 → 校验失败不会白白踢掉旧连接。
	c.hub.becomeOwner(onlineReq.MachineKey, c)

	// [FIX 2026-07-28 B] 若在校验/接管期间本连接又被"更新的连接"顶替(自身 superseded=true)，放弃写记录：
	// 不覆盖新所有者刚写的记录。本连接随后被关闭，其清理经 releaseOwner 判定已非所有者、不动记录。
	if c.superseded.Load() {
		return uint32(types.ErrCodeOnline), "superseded by a newer connection during online", []byte("")
	}

	// 传 c.ClientID：MachineConnected upsert 写入本连接 conn_id，供退出时连接感知删除。
	if err := db.MDB.MachineConnected(ctx, onlineReq.MachineKey, c.ClientID); err != nil {
		// [FIX 2026-07-28 B] 写记录失败(DB 抖动/唯一索引 E11000)：既要撤掉 becomeOwner 抢到的所有权，也要
		//   【关掉本连接】。若只 releaseOwner 不关连接，会留下"socket 还活着、但内存里非所有者"的状态：下线复核
		//   用 isMachineOwned 判定→它=未拥有→若此前有待触发的下线元素、会对这台"其实活着的机器"误触发下线。
		//   关掉连接后本 socket 不再是"活着的未拥有连接"，客户端收到错误会在新 socket 重连重试上线；关连接触发
		//   readPump 退出，其 releaseOwner 已释放故为 no-op、不误删记录/不误排下线。
		c.hub.releaseOwner(onlineReq.MachineKey, c)
		// [FIX 2026-07-28 B] 补排一次延迟下线检查：本连接是"顶替旧所有者后才写记录失败"的情况——旧所有者
		//   退出时因已非所有者不会排下线、本连接又失败退出，若不补这一下，机器此刻若真永久掉线就再没人给它
		//   排下线(死机漏惩罚/漏退租)。补排后:机器若重连→新连接的 connect 取消它;若真掉线→5min 复核 isMachineOwned
		//   =未拥有→触发下线(仍经主后端在线校验兜底,不会误惩罚活机)。机器已过 GetMachineState 注册/在租门槛,是真机器。
		select {
		case c.hub.do.diconnect <- delayOfflineChanInfo{
			machine:        onlineReq.MachineKey,
			disconnectTime: time.Now(),
			stakingType:    onlineReq.StakingType,
		}:
		case <-c.hub.do.stopped:
		}
		c.conn.Close()
		return uint32(types.ErrCodeDatabase), fmt.Sprintf("upsert online database failed: %v", err), []byte("")
	}
	// c.hub.wsConns.Store(c, struct{}{})
	// select stopped 防 HandleDelayOffline 已退出后向无接收方 channel 发送而阻塞
	select {
	case c.hub.do.connect <- delayOfflineChanInfo{
		machine:        c.MachineKey,
		disconnectTime: time.Now(),
		stakingType:    c.StakingType,
	}:
	case <-c.hub.do.stopped:
	}
	return 0, "machine online success", []byte("")
}

func (c *Client) handleDeepLinkMachineInfoSTRequest(ctx context.Context, req *types.WsRequest) (uint32, string, []byte) {
	if c.MachineKey.MachineId == "" {
		return uint32(types.ErrCodeMachineInfo), "machine id is empty, need send online request first", []byte("")
	}

	miReq := types.DeepLinkMachineInfoST{}
	if err := json.Unmarshal(req.Body, &miReq); err != nil {
		return uint32(types.ErrCodeParam), fmt.Sprintf("parse machine info request failed: %v", err), []byte("")
	}

	if err := miReq.Validate(); err != nil {
		return uint32(types.ErrCodeParam), err.Error(), []byte("")
	}
	miReq.ClientIP = c.ClientIP

	ctx, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()
	mi, err := db.MDB.GetMachineInfo(ctx, c.MachineKey)
	if err != nil {
		return uint32(types.ErrCodeDatabase), fmt.Sprintf("get machine info from database failed: %v", err), []byte("")
	} else if mi.MDBDeepLinkMachineInfoST.CalcPoint == 0 {
		calcPoint, err := calculator.CalculatePointExactFromReport(
			miReq.GPUNames,
			miReq.GPUMemoryTotal,
			miReq.MemoryTotal,
		)
		if calcPoint == 0 {
			log.Log.WithFields(logrus.Fields{
				"uuid":    c.ClientID,
				"machine": c.MachineKey,
			}).Errorf("calculate gpu point from report %v failed %v", miReq, err)
			calcPoint, err = calculator.CalculatePointFuzzyFromReport(
				miReq.GPUNames,
				miReq.MemoryTotal,
			)
		}

		if calcPoint == 0 {
			log.Log.WithFields(logrus.Fields{
				"uuid":    c.ClientID,
				"machine": c.MachineKey,
			}).Errorf("calculate gpu point from report %v failed %v", miReq, err)
			return uint32(types.ErrCodeMachineInfo), fmt.Sprintf("calculate gpu point failed %v", err), []byte("")
		} else {
			log.Log.WithFields(logrus.Fields{
				"uuid":    c.ClientID,
				"machine": c.MachineKey,
			}).Infof("calculate gpu point from reported machine info %v => %v", miReq, calcPoint)

			loc, err := db.GetPositionOfIP(c.ClientIP)
			if err != nil {
				log.Log.WithFields(logrus.Fields{
					"uuid":    c.ClientID,
					"machine": c.MachineKey,
				}).Errorf("get location err %v from ip address %v", err, c.ClientIP)
			} else {
				log.Log.WithFields(logrus.Fields{
					"uuid":    c.ClientID,
					"machine": c.MachineKey,
				}).Infof("get location (%f, %f) from ip address %v", loc.Longitude, loc.Latitude, c.ClientIP)
			}

			if hash, err := dbc.DbcChain.SetDeepLinkMachineInfoST(
				ctx,
				c.MachineKey,
				miReq,
				int64(calcPoint*10000),
				loc.Longitude,
				loc.Latitude,
				loc.Region,
			); err != nil {
				errMsg := fmt.Sprintf(
					"set machine info in chain contract with hash %v failed: %v",
					hash,
					err,
				)
				return uint32(types.ErrCodeDbcChain), errMsg, []byte("")
			} else {
				log.Log.WithFields(logrus.Fields{
					"uuid":    c.ClientID,
					"machine": c.MachineKey,
				}).Info("set machine info in chain contract success with hash ", hash)

				if err := db.MDB.SetMachineInfo(
					ctx,
					c.MachineKey,
					&types.MDBDeepLinkMachineInfoST{
						DeepLinkMachineInfoST: miReq,
						CalcPoint:             calcPoint,
						Longitude:             loc.Longitude,
						Latitude:              loc.Latitude,
						Region:                loc.Region,
					},
					nil,
				); err != nil {
					return uint32(types.ErrCodeDbcChain), fmt.Sprintf("set machine info in database failed %v", err), []byte("")
				} else {
					log.Log.WithFields(logrus.Fields{
						"uuid":    c.ClientID,
						"machine": c.MachineKey,
					}).Info("set machine info in database success")
				}
			}
		}
	} else {
		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).WithField("machine info", miReq).Info("get machine info success and calcpoint ", mi.MDBDeepLinkMachineInfoST.CalcPoint)
	}

	if err := db.MDB.AddMachineTM(
		ctx,
		types.MDBMachineTM{
			Timestamp:                    time.UnixMilli(req.Timestamp),
			Machine:                      c.MachineKey,
			DeepLinkMachineInfoST:        miReq,
			DeepLinkMachineInfoBandwidth: types.DeepLinkMachineInfoBandwidth{},
		},
	); err != nil {
		return uint32(types.ErrCodeDatabase), fmt.Sprintf("add machine tm in database failed %v", err), []byte("")
	}

	// pm.SetMetrics(nodeId, miReq)
	return 0, "add machine tm success", []byte("")
}

func (c *Client) handleDeepLinkMachineInfoBandwidthRequest(ctx context.Context, req *types.WsRequest) (uint32, string, []byte) {
	if c.MachineKey.MachineId == "" {
		return uint32(types.ErrCodeMachineInfo), "machine id is empty, need send online request first", []byte("")
	}

	miReq := types.DeepLinkMachineInfoBandwidth{}
	if err := json.Unmarshal(req.Body, &miReq); err != nil {
		return uint32(types.ErrCodeParam), fmt.Sprintf("parse machine info bandwidth request failed: %v", err), []byte("")
	}

	if err := miReq.Validate(); err != nil {
		return uint32(types.ErrCodeParam), err.Error(), []byte("")
	}
	miReq.ClientIP = c.ClientIP

	ctx, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()
	mi, err := db.MDB.GetMachineInfo(ctx, c.MachineKey)
	if err != nil {
		return uint32(types.ErrCodeDatabase), fmt.Sprintf("get machine info bandwidth from database failed: %v", err), []byte("")
	} else if mi.MDBDeepLinkMachineInfoBandwidth.Bandwidth == 0 {
		loc, err := db.GetPositionOfIP(c.ClientIP)
		if err != nil {
			log.Log.WithFields(logrus.Fields{
				"uuid":    c.ClientID,
				"machine": c.MachineKey,
			}).Errorf("get location err %v from ip address %v", err, c.ClientIP)
		} else {
			log.Log.WithFields(logrus.Fields{
				"uuid":    c.ClientID,
				"machine": c.MachineKey,
			}).Infof("get location (%f, %f) from ip address %v", loc.Longitude, loc.Latitude, c.ClientIP)
		}
		region := db.GetBandwidthRegion(&loc)

		if hash, err := dbc.DbcChain.SetDeepLinkMachineInfoBandwidth(
			ctx,
			c.MachineKey,
			miReq,
			region,
		); err != nil {
			errMsg := fmt.Sprintf(
				"set machine info bandwidth in chain contract with hash %v failed: %v",
				hash,
				err,
			)
			return uint32(types.ErrCodeDbcChain), errMsg, []byte("")
		} else {
			log.Log.WithFields(logrus.Fields{
				"uuid":    c.ClientID,
				"machine": c.MachineKey,
			}).Info("set machine info bandwidth in chain contract success with hash ", hash)

			if err := db.MDB.SetMachineInfo(
				ctx,
				c.MachineKey,
				nil,
				&types.MDBDeepLinkMachineInfoBandwidth{
					DeepLinkMachineInfoBandwidth: miReq,
					Region:                       region,
				},
			); err != nil {
				return uint32(types.ErrCodeDbcChain), fmt.Sprintf("set machine info bandwidth in database failed %v", err), []byte("")
			} else {
				log.Log.WithFields(logrus.Fields{
					"uuid":    c.ClientID,
					"machine": c.MachineKey,
				}).Info("set machine info bandwidth in database success")
			}
		}
	} else {
		log.Log.WithFields(logrus.Fields{
			"uuid":    c.ClientID,
			"machine": c.MachineKey,
		}).WithField("machine info", miReq).Info(
			"get machine info bandwidth success and bandwidth ",
			mi.MDBDeepLinkMachineInfoBandwidth.Bandwidth,
		)
	}

	if err := db.MDB.AddMachineTM(
		ctx,
		types.MDBMachineTM{
			Timestamp:                    time.UnixMilli(req.Timestamp),
			Machine:                      c.MachineKey,
			DeepLinkMachineInfoST:        types.DeepLinkMachineInfoST{},
			DeepLinkMachineInfoBandwidth: miReq,
		},
	); err != nil {
		return uint32(types.ErrCodeDatabase), fmt.Sprintf("add machine bandwidth tm in database failed %v", err), []byte("")
	}

	// pm.SetMetrics(nodeId, miReq)
	return 0, "add machine bandwidth tm success", []byte("")
}

func Ws2(hub *Hub, ctx *gin.Context, wsCtx context.Context) {
	w, r := ctx.Writer, ctx.Request
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Upgrade to websocket failed", http.StatusUpgradeRequired)
		log.Log.Error("Upgrade to websocket failed: ", err)
		return
	}

	randomUUID, err := uuid.NewRandom()
	if err != nil {
		ctx.JSON(
			http.StatusInternalServerError,
			types.BaseHttpResponse{
				Code:    int(types.ErrCodeUUID),
				Message: types.ErrCodeUUID.String(),
			},
		)
		log.Log.Error("generate uuid for websocket connection failed: ", err)
		return
	}

	client := &Client{
		hub:      hub,
		conn:     c,
		send:     make(chan envelope, 32),
		sendDone: make(chan bool),
		ClientIP: ctx.ClientIP(),
		ClientID: randomUUID.String(),
	}
	hub.wsConns.Store(
		client,
		struct{}{},
	)

	go client.writePump(wsCtx)
	// go client.readPump(context.Background())
	client.readPump(context.Background())
}
