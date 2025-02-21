package ws

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"DistributedDetectionNode/db"
	"DistributedDetectionNode/dbc"
	hmp "DistributedDetectionNode/http"
	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
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

var wsConns sync.Map

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 30 * time.Second // 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

func ShutdownAllWsConns() {
	wsConns.Range(func(key, value any) bool {
		if conn, ok := key.(*websocket.Conn); ok {
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			conn.Close()
		}
		return true
	})
	log.Log.Println("Shutdownd all websocket connections")
}

func Ws(ctx *gin.Context, pm *hmp.PrometheusMetrics) {
	w, r := ctx.Writer, ctx.Request
	var wsConnInfo types.WsConnInfo
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Upgrade to websocket failed", http.StatusUpgradeRequired)
		log.Log.Error("Upgrade to websocket failed: ", err)
		return
	}
	defer func() {
		log.Log.WithFields(logrus.Fields{
			"machine": wsConnInfo.MachineKey,
		}).Info("connection stopped")
		c.Close()
	}()

	c.SetReadDeadline(time.Now().Add(pongWait))
	c.SetPingHandler(func(appData string) error {
		c.SetReadDeadline(time.Now().Add(pongWait))
		log.Log.WithFields(logrus.Fields{
			"machine": wsConnInfo.MachineKey,
		}).Debug("ping handler")
		err := c.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(writeWait))
		if err == websocket.ErrCloseSent {
			return nil
		} else if e, ok := err.(net.Error); ok && e.Temporary() {
			return nil
		}
		return err
	})

	wsConnInfo.ClientIP = ctx.ClientIP() // c.RemoteAddr()

	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			log.Log.WithFields(logrus.Fields{
				"machine": wsConnInfo.MachineKey,
			}).Info("read: ", err)
			break
		}
		log.Log.WithFields(logrus.Fields{
			"machine": wsConnInfo.MachineKey,
		}).Infof("recv message: %v %s", mt, message)

		req := &types.WsRequest{}
		if err := json.Unmarshal(message, req); err != nil {
			log.Log.WithFields(logrus.Fields{
				"machine": wsConnInfo.MachineKey,
			}).Error("parse request failed: ", err)
			writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
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

		handleWsRequest(r.Context(), c, &wsConnInfo, req, pm)
	}

	if wsConnInfo.MachineId != "" {
		ctx1, cancel1 := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel1()
		if hash, err := dbc.DbcChain.Report(
			ctx1,
			types.MachineOffline,
			wsConnInfo.StakingType,
			wsConnInfo.Project,
			wsConnInfo.MachineId,
		); err != nil {
			log.Log.WithFields(logrus.Fields{
				"machine": wsConnInfo.MachineKey,
			}).Errorf(
				"machine offline in chain contract failed with hash %v because of %v",
				hash,
				err,
			)
		} else {
			log.Log.WithFields(logrus.Fields{
				"machine": wsConnInfo.MachineKey,
			}).Info("machine offline in chain contract success with hash ", hash)
		}
		db.MDB.MachineOffline(r.Context(), wsConnInfo.MachineKey)
		// pm.DeleteMetrics(machine)
	}
	wsConns.Delete(c)
}

func writeWsResponse(c *websocket.Conn, machine types.MachineKey, res *types.WsResponse) error {
	resBytes, err := json.Marshal(res)
	if err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
		}).Error("marshal reponse failed: ", err)
		return err
	}
	err = c.WriteMessage(websocket.TextMessage, resBytes)
	if err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
		}).Error("write response message failed: ", err)
		return err
	}
	return nil
}
