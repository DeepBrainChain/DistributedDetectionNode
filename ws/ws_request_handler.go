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

func handleWsRequest(ctx context.Context, c *websocket.Conn, machine *types.WsOnlineRequest, req *types.WsRequest, pm *hmp.PrometheusMetrics) error {
	switch req.Type {
	case uint32(types.WsMtOnline):
		handleWsOnlineRequest(ctx, c, machine, req, pm)
	case uint32(types.WsMtMachineInfo):
		handleWsMachineInfoRequest(ctx, c, *machine, req, pm)
	default:
		log.Log.WithFields(logrus.Fields{
			"machine": *machine,
		}).Error("unknowned request message type")
		writeWsResponse(c, *machine, &types.WsResponse{
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

func handleWsOnlineRequest(ctx context.Context, c *websocket.Conn, machine *types.WsOnlineRequest, req *types.WsRequest, pm *hmp.PrometheusMetrics) error {
	if machine.MachineId != "" {
		writeWsResponse(c, *machine, &types.WsResponse{
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
			"machine": *machine,
		}).Error("device has been online, repeated requests")
		return nil
	}

	onlineReq := &types.WsOnlineRequest{}
	if err := json.Unmarshal(req.Body, onlineReq); err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": *machine,
		}).Error("parse online request failed: ", err)
		writeWsResponse(c, *machine, &types.WsResponse{
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
	if db.MDB.IsMachineOnline(ctx1, types.MachineKey(*onlineReq)) {
		writeWsResponse(c, *onlineReq, &types.WsResponse{
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
			"machine": onlineReq,
		}).Error("device has been online, repeated connection")
		return nil
	}

	ctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	if err := db.MDB.MachineOnline(ctx2, types.MachineKey(*onlineReq)); err != nil {
		writeWsResponse(c, *onlineReq, &types.WsResponse{
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

	*machine = *onlineReq
	writeWsResponse(c, *machine, &types.WsResponse{
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

func handleWsMachineInfoRequest(ctx context.Context, c *websocket.Conn, machine types.WsOnlineRequest, req *types.WsRequest, pm *hmp.PrometheusMetrics) error {
	if machine.MachineId == "" {
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
		}).Error("node id is empty, need online device first")
		writeWsResponse(c, machine, &types.WsResponse{
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
			"machine": machine,
		}).Error("parse machine info request failed: ", err)
		writeWsResponse(c, machine, &types.WsResponse{
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

	ctx1, cancel1 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel1()
	mi, err := db.MDB.GetMachineInfo(ctx1, types.MachineKey(machine))
	if err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": machine,
		}).Error("get machine info from database failed: ", err)
	} else if mi.CalcPoint == 0 {
		//
	}

	ctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	if err := db.MDB.AddMachineTM(ctx2, types.MachineKey(machine), time.UnixMilli(req.Timestamp), miReq); err != nil {
		writeWsResponse(c, machine, &types.WsResponse{
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

	// pm.SetMetrics(nodeId, miReq)
	log.Log.WithFields(logrus.Fields{
		"machine": machine,
	}).WithField("machine info", miReq).Info("update machine info")
	writeWsResponse(c, machine, &types.WsResponse{
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
