package ws

import (
	"context"
	"encoding/json"
	"time"

	"DistributedDetectionNode/db"
	hmp "DistributedDetectionNode/http"
	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

func handleWsRequest(ctx context.Context, c *websocket.Conn, nodeId *string, req *types.WsRequest, pm *hmp.PrometheusMetrics) error {
	switch req.Type {
	case uint32(types.WsMtOnline):
		handleWsOnlineRequest(ctx, c, nodeId, req, pm)
	case uint32(types.WsMtMachineInfo):
		handleWsMachineInfoRequest(ctx, c, *nodeId, req, pm)
	default:
		log.Log.WithFields(logrus.Fields{
			"node_id": *nodeId,
		}).Error("unknowned request message type")
		writeWsResponse(c, *nodeId, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeParam),
			Message: "unknowned request message type",
			Body:    []byte(""),
		})
	}
	return nil
}

func handleWsOnlineRequest(ctx context.Context, c *websocket.Conn, nodeId *string, req *types.WsRequest, pm *hmp.PrometheusMetrics) error {
	if *nodeId != "" {
		writeWsResponse(c, *nodeId, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeOnline),
			Message: "device has been online, repeated requests",
			Body:    []byte(""),
		})
		log.Log.WithFields(logrus.Fields{
			"node_id": *nodeId,
		}).Error("device has been online, repeated requests")
		return nil
	}

	onlineReq := &types.WsOnlineRequest{}
	if err := json.Unmarshal(req.Body, onlineReq); err != nil {
		log.Log.WithFields(logrus.Fields{
			"node_id": *nodeId,
		}).Error("parse online request failed: ", err)
		writeWsResponse(c, *nodeId, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeParam),
			Message: "parse online request failed",
			Body:    []byte(""),
		})
		return nil
	}

	ctx1, cancel1 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel1()
	if db.MDB.IsNodeOnline(ctx1, onlineReq.NodeId) {
		writeWsResponse(c, onlineReq.NodeId, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeOnline),
			Message: "device has been online, repeated connection",
			Body:    []byte(""),
		})
		log.Log.WithFields(logrus.Fields{
			"node_id": onlineReq.NodeId,
		}).Error("device has been online, repeated connection")
		return nil
	}

	ctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	if err := db.MDB.NodeOnline(ctx2, onlineReq.NodeId); err != nil {
		writeWsResponse(c, onlineReq.NodeId, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeDatabase),
			Message: "insert online database failed",
			Body:    []byte(""),
		})
		return nil
	}

	*nodeId = onlineReq.NodeId
	writeWsResponse(c, *nodeId, &types.WsResponse{
		WsHeader: types.WsHeader{
			Version:   0,
			Timestamp: time.Now().Unix(),
			Id:        req.Id,
			Type:      req.Type,
			PubKey:    []byte(""),
			Sign:      []byte(""),
		},
		Code:    0,
		Message: "ok",
		Body:    []byte(""),
	})
	return nil
}

func handleWsMachineInfoRequest(ctx context.Context, c *websocket.Conn, nodeId string, req *types.WsRequest, pm *hmp.PrometheusMetrics) error {
	if nodeId == "" {
		log.Log.WithFields(logrus.Fields{
			"node_id": nodeId,
		}).Error("node id is empty, need online device first")
		writeWsResponse(c, nodeId, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeMachineInfo),
			Message: "node id is empty, need send online device first",
			Body:    []byte(""),
		})
		return nil
	}

	miReq := types.WsMachineInfoRequest{}
	if err := json.Unmarshal(req.Body, &miReq); err != nil {
		log.Log.WithFields(logrus.Fields{
			"node_id": nodeId,
		}).Error("parse machine info request failed: ", err)
		writeWsResponse(c, nodeId, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeParam),
			Message: "parse machine info request failed",
			Body:    []byte(""),
		})
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.MDB.AddDeviceInfo(ctx, nodeId, time.UnixMilli(req.Timestamp), miReq); err != nil {
		writeWsResponse(c, nodeId, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeDatabase),
			Message: "update database failed",
			Body:    []byte(""),
		})
		return nil
	}

	pm.SetMetrics(nodeId, miReq)
	log.Log.WithFields(logrus.Fields{
		"node_id": nodeId,
	}).WithField("machine info", miReq).Info("update machine info")
	writeWsResponse(c, nodeId, &types.WsResponse{
		WsHeader: types.WsHeader{
			Version:   0,
			Timestamp: time.Now().Unix(),
			Id:        req.Id,
			Type:      req.Type,
			PubKey:    []byte(""),
			Sign:      []byte(""),
		},
		Code:    0,
		Message: "ok",
		Body:    []byte(""),
	})
	return nil
}
