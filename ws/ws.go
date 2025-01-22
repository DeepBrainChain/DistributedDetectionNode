package ws

import (
	"encoding/json"
	"net/http"
	"time"

	"DistributedDetectionNode/db"
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

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 30 * time.Second // 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

func Ws(ctx *gin.Context, pm *hmp.PrometheusMetrics) {
	w, r := ctx.Writer, ctx.Request
	var machine types.WsOnlineRequest
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Upgrade to websocket failed", http.StatusUpgradeRequired)
		log.Log.Error("Upgrade to websocket failed: ", err)
		return
	}
	defer func() {
		if machine.MachineId != "" {
			db.MDB.MachineOffline(r.Context(), types.MachineKey(machine))
			// pm.DeleteMetrics(machine)
		}
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
		}).Info("connection stopped")
		c.Close()
	}()

	c.SetReadDeadline(time.Now().Add(pongWait))
	c.SetPingHandler(func(appData string) error {
		c.SetReadDeadline(time.Now().Add(pongWait))
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
		}).Info("ping handler")
		return nil
	})

	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			log.Log.WithFields(logrus.Fields{
				"machine": machine,
			}).Info("read: ", err)
			break
		}
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
		}).Infof("recv message: %v %s", mt, message)

		req := &types.WsRequest{}
		if err := json.Unmarshal(message, req); err != nil {
			log.Log.WithFields(logrus.Fields{
				"machine": machine,
			}).Error("parse request failed: ", err)
			writeWsResponse(c, machine, &types.WsResponse{
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

		handleWsRequest(r.Context(), c, &machine, req, pm)
	}
}

func writeWsResponse(c *websocket.Conn, machine types.WsOnlineRequest, res *types.WsResponse) error {
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
