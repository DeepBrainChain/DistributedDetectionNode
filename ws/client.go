package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

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
	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	MachineKey  types.MachineKey
	StakingType types.StakingType
	ClientIP    string
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump(ctx context.Context) {
	Hub.wg.Add(1)
	defer func() {
		Hub.wg.Done()
		Hub.wsConns.Delete(c)
		close(c.send)
		c.conn.Close()
	}()
	// c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	// c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	c.conn.SetPingHandler(func(appData string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		log.Log.WithFields(logrus.Fields{
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
					"machine": c.MachineKey,
				}).Error("read error: ", err)
			}
			break
		}
		log.Log.WithFields(logrus.Fields{
			"machine": c.MachineKey,
		}).Infof("recv message: %v %s", mt, message)

		// message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		// c.hub.broadcast <- message
		req := &types.WsRequest{}
		if err := json.Unmarshal(message, req); err != nil {
			log.Log.WithFields(logrus.Fields{
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

		go c.handleRequest(ctx, req)
	}

	if c.MachineKey.MachineId != "" {
		// ctx1, cancel1 := context.WithTimeout(ctx, 60*time.Second)
		// defer cancel1()
		// if hash, err := dbc.DbcChain.Report(
		// 	ctx1,
		// 	types.MachineOffline,
		// 	c.StakingType,
		// 	c.MachineKey.Project,
		// 	c.MachineKey.MachineId,
		// ); err != nil {
		// 	log.Log.WithFields(logrus.Fields{
		// 		"machine": c.MachineKey,
		// 	}).Errorf(
		// 		"machine offline in chain contract failed with hash %v because of %v",
		// 		hash,
		// 		err,
		// 	)
		// } else {
		// 	log.Log.WithFields(logrus.Fields{
		// 		"machine": c.MachineKey,
		// 	}).Info("machine offline in chain contract success with hash ", hash)
		// }
		db.MDB.MachineOffline(ctx, c.MachineKey)
		_, err := db.MDB.GetMachineInfo(ctx, c.MachineKey)
		if err == nil {
			Hub.do.diconnect <- delayOfflineChanInfo{
				machine:        c.MachineKey,
				disconnectTime: time.Now(),
				stakingType:    c.StakingType,
			}
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
	Hub.wg.Add(1)
	defer func() {
		Hub.wg.Done()
		c.conn.Close()
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

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued chat messages to the current websocket message.
			// n := len(c.send)
			// for i := 0; i < n; i++ {
			// 	w.Write(newline)
			// 	w.Write(<-c.send)
			// }

			if err := w.Close(); err != nil {
				return
			}
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
			"machine": c.MachineKey,
		}).Errorf("marshal reponse with id %v failed: %v", res.Id, err)
		return err
	}
	c.send <- resBytes
	return nil
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
	case uint32(types.WsMtMachineInfo):
		code, message, body = c.handleMachineInfoRequest(ctx, req)
	default:
		code = uint32(types.ErrCodeParam)
		message = "unknowned request message type"
		body = []byte("")
	}
	if code == 0 {
		log.Log.WithFields(logrus.Fields{
			"machine": c.MachineKey,
		}).Info(message)
	} else {
		log.Log.WithFields(logrus.Fields{
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
		return uint32(types.ErrCodeParam), fmt.Sprintf("parse online request failed: %f", err), []byte("")
	}

	ctx1, cancel1 := context.WithTimeout(ctx, 20*time.Second)
	defer cancel1()
	if db.MDB.IsMachineOnline(ctx1, onlineReq.MachineKey) {
		return uint32(types.ErrCodeOnline), "machine has been online, repeated connection", []byte("")
	}

	_, err := db.MDB.GetMachineInfo(ctx1, onlineReq.MachineKey)
	if err != nil {
		return uint32(types.ErrCodeOnline), "machine not registered", []byte("")
	}

	{
		ctx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
		defer cancel2()
		if hash, err := dbc.DbcChain.Report(
			ctx2,
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
				"machine": onlineReq,
			}).Info("machine online in chain contract success with hash ", hash)
		}
	}

	ctx2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	if err := db.MDB.MachineOnline(ctx2, onlineReq.MachineKey); err != nil {
		return uint32(types.ErrCodeDatabase), fmt.Sprintf("insert online database failed: %v", err), []byte("")
	}

	c.MachineKey = onlineReq.MachineKey
	c.StakingType = onlineReq.StakingType
	// Hub.wsConns.Store(c, struct{}{})
	Hub.do.connect <- delayOfflineChanInfo{
		machine:        c.MachineKey,
		disconnectTime: time.Now(),
		stakingType:    c.StakingType,
	}
	return 0, "machine online success", []byte("")
}

func (c *Client) handleMachineInfoRequest(ctx context.Context, req *types.WsRequest) (uint32, string, []byte) {
	if c.MachineKey.MachineId == "" {
		return uint32(types.ErrCodeMachineInfo), "machine id is empty, need send online request first", []byte("")
	}

	miReq := types.WsMachineInfoRequest{}
	if err := json.Unmarshal(req.Body, &miReq); err != nil {
		return uint32(types.ErrCodeParam), fmt.Sprintf("parse machine info request failed: %v", err), []byte("")
	}
	miReq.ClientIP = c.ClientIP

	ctx1, cancel1 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel1()
	mi, err := db.MDB.GetMachineInfo(ctx1, c.MachineKey)
	if err != nil {
		return uint32(types.ErrCodeDatabase), fmt.Sprintf("get machine info from database failed: %v", err), []byte("")
	} else if mi.CalcPoint == 0 {
		calcPoint, err := calculator.CalculatePointFromReport(
			miReq.GPUNames,
			miReq.GPUMemoryTotal,
			miReq.MemoryTotal,
		)
		if calcPoint == 0 {
			log.Log.WithFields(logrus.Fields{
				"machine": c.MachineKey,
			}).Errorf("calculate gpu point from report %v failed %v", miReq, err)
			return uint32(types.ErrCodeMachineInfo), fmt.Sprintf("calculate gpu point failed %v", err), []byte("")
		} else {
			log.Log.WithFields(logrus.Fields{
				"machine": c.MachineKey,
			}).Infof("calculate gpu point from reported machine info %v => %v", miReq, calcPoint)

			loc, err := db.GetPositionOfIP(c.ClientIP)
			if err != nil {
				log.Log.WithFields(logrus.Fields{
					"machine": c.MachineKey,
				}).Errorf("get location err %v from ip address %v", err, c.ClientIP)
			} else {
				log.Log.WithFields(logrus.Fields{
					"machine": c.MachineKey,
				}).Infof("get location (%f, %f) from ip address %v", loc.Longitude, loc.Latitude, c.ClientIP)
			}

			ctx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
			defer cancel2()
			if hash, err := dbc.DbcChain.SetMachineInfo(
				ctx2,
				c.MachineKey,
				types.MachineInfo(miReq),
				int64(calcPoint*10000),
				loc.Longitude,
				loc.Latitude,
			); err != nil {
				errMsg := fmt.Sprintf(
					"set machine info in chain contract with hash %v failed: %v",
					hash,
					err,
				)
				return uint32(types.ErrCodeDbcChain), errMsg, []byte("")
			} else {
				log.Log.WithFields(logrus.Fields{
					"machine": c.MachineKey,
				}).Info("set machine info in chain contract success with hash ", hash)

				ctx3, cancel3 := context.WithTimeout(ctx, 10*time.Second)
				defer cancel3()
				if err := db.MDB.SetMachineInfo(
					ctx3,
					c.MachineKey,
					miReq,
					calcPoint,
					loc.Longitude,
					loc.Latitude,
				); err != nil {
					return uint32(types.ErrCodeDbcChain), fmt.Sprintf("set machine info in database failed %v", err), []byte("")
				} else {
					log.Log.WithFields(logrus.Fields{
						"machine": c.MachineKey,
					}).Info("set machine info in database success")
				}
			}
		}
	} else {
		log.Log.WithFields(logrus.Fields{
			"machine": c.MachineKey,
		}).WithField("machine info", miReq).Info("get machine info success and calcpoint ", mi.CalcPoint)
	}

	ctx2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	if err := db.MDB.AddMachineTM(
		ctx2,
		c.MachineKey,
		time.UnixMilli(req.Timestamp),
		miReq,
	); err != nil {
		return uint32(types.ErrCodeDatabase), fmt.Sprintf("add machine tm in database failed %v", err), []byte("")
	}

	// pm.SetMetrics(nodeId, miReq)
	return 0, "add machine tm success", []byte("")
}

func Ws2(ctx *gin.Context, wsCtx context.Context) {
	w, r := ctx.Writer, ctx.Request
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Upgrade to websocket failed", http.StatusUpgradeRequired)
		log.Log.Error("Upgrade to websocket failed: ", err)
		return
	}

	client := &Client{
		conn:     c,
		send:     make(chan []byte, 512),
		ClientIP: ctx.ClientIP(),
	}
	Hub.wsConns.Store(
		client,
		struct{}{},
	)

	go client.writePump(wsCtx)
	go client.readPump(wsCtx)
}
