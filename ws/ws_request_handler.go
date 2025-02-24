package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"DistributedDetectionNode/db"
	"DistributedDetectionNode/dbc"
	"DistributedDetectionNode/dbc/calculator"
	hmp "DistributedDetectionNode/http"
	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

func handleWsRequest(
	ctx context.Context,
	c *websocket.Conn,
	wsConnInfo *types.WsConnInfo,
	req *types.WsRequest,
	pm *hmp.PrometheusMetrics,
) error {
	switch req.Type {
	case uint32(types.WsMtOnline):
		handleWsOnlineRequest(ctx, c, wsConnInfo, req, pm)
	case uint32(types.WsMtMachineInfo):
		handleWsMachineInfoRequest(ctx, c, *wsConnInfo, req, pm)
	default:
		log.Log.WithFields(logrus.Fields{
			"machine": wsConnInfo.MachineKey,
		}).Error("unknowned request message type")
		writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
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

func handleWsOnlineRequest(
	ctx context.Context,
	c *websocket.Conn,
	wsConnInfo *types.WsConnInfo,
	req *types.WsRequest,
	pm *hmp.PrometheusMetrics,
) error {
	if wsConnInfo.MachineId != "" {
		writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
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
			"machine": wsConnInfo.MachineKey,
		}).Error("device has been online, repeated requests")
		return nil
	}

	onlineReq := &types.WsOnlineRequest{}
	if err := json.Unmarshal(req.Body, onlineReq); err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": *wsConnInfo,
		}).Error("parse online request failed: ", err)
		writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
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

	ctx1, cancel1 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel1()
	if db.MDB.IsMachineOnline(ctx1, onlineReq.MachineKey) {
		writeWsResponse(c, onlineReq.MachineKey, &types.WsResponse{
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
			writeWsResponse(c, onlineReq.MachineKey, &types.WsResponse{
				WsHeader: types.WsHeader{
					Version:   0,
					Timestamp: time.Now().Unix(),
					Id:        req.Id,
					Type:      req.Type,
					PubKey:    []byte(""),
					Sign:      []byte(""),
				},
				Code:    uint32(types.ErrCodeDbcChain),
				Message: errMsg,
				Body:    []byte(""),
			})
			log.Log.WithFields(logrus.Fields{
				"machine": onlineReq,
			}).Error(errMsg)
			return nil
		} else {
			log.Log.WithFields(logrus.Fields{
				"machine": onlineReq,
			}).Info("machine online in chain contract success with hash ", hash)
		}
	}

	ctx2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	if err := db.MDB.MachineOnline(ctx2, onlineReq.MachineKey); err != nil {
		writeWsResponse(c, onlineReq.MachineKey, &types.WsResponse{
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

	wsConnInfo.MachineKey = onlineReq.MachineKey
	wsConnInfo.StakingType = onlineReq.StakingType
	wsConns.Store(c, struct{}{})
	writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
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

func handleWsMachineInfoRequest(
	ctx context.Context,
	c *websocket.Conn,
	wsConnInfo types.WsConnInfo,
	req *types.WsRequest,
	pm *hmp.PrometheusMetrics,
) error {
	if wsConnInfo.MachineId == "" {
		log.Log.WithFields(logrus.Fields{
			"machine": wsConnInfo.MachineKey,
		}).Error("node id is empty, need online device first")
		writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
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
			"machine": wsConnInfo.MachineKey,
		}).Error("parse machine info request failed: ", err)
		writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
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
	miReq.ClientIP = wsConnInfo.ClientIP

	ctx1, cancel1 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel1()
	mi, err := db.MDB.GetMachineInfo(ctx1, wsConnInfo.MachineKey)
	if err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": wsConnInfo.MachineKey,
		}).Error("get machine info from database failed: ", err)
	} else if mi.CalcPoint == 0 {
		calcPoint, err := calculator.CalculatePointFromReport(
			miReq.GPUNames,
			miReq.GPUMemoryTotal,
			miReq.MemoryTotal,
		)
		if calcPoint == 0 {
			log.Log.WithFields(logrus.Fields{
				"machine": wsConnInfo.MachineKey,
			}).Errorf("calculate gpu point from report %v failed %v", miReq, err)
			writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
				WsHeader: types.WsHeader{
					Version:   0,
					Timestamp: time.Now().Unix(),
					Id:        req.Id,
					Type:      req.Type,
					PubKey:    []byte(""),
					Sign:      []byte(""),
				},
				Code:    uint32(types.ErrCodeMachineInfo),
				Message: fmt.Sprintf("calculate gpu point failed %v", err),
				Body:    []byte(""),
			})
			return nil
		} else {
			log.Log.WithFields(logrus.Fields{
				"machine": wsConnInfo.MachineKey,
			}).Infof("calculate gpu point from reported machine info %v => %v", miReq, calcPoint)

			longitude, latitude, err := db.GetPositionOfIP(miReq.ClientIP)
			log.Log.WithFields(logrus.Fields{
				"machine": wsConnInfo.MachineKey,
			}).Infof("get location (%f, %f) from ip address %v", longitude, latitude, err)

			ctx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
			defer cancel2()
			if hash, err := dbc.DbcChain.SetMachineInfo(
				ctx2,
				wsConnInfo.MachineKey,
				types.MachineInfo(miReq),
				int64(calcPoint*10000),
				longitude,
				latitude,
			); err != nil {
				errMsg := fmt.Sprintf(
					"set machine info in chain contract with hash %v failed: %v",
					hash,
					err,
				)
				log.Log.WithFields(logrus.Fields{
					"machine": wsConnInfo.MachineKey,
				}).Error(errMsg)
				writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
					WsHeader: types.WsHeader{
						Version:   0,
						Timestamp: time.Now().Unix(),
						Id:        req.Id,
						Type:      req.Type,
						PubKey:    []byte(""),
						Sign:      []byte(""),
					},
					Code:    uint32(types.ErrCodeDbcChain),
					Message: errMsg,
					Body:    []byte(""),
				})
				return nil
			} else {
				log.Log.WithFields(logrus.Fields{
					"machine": wsConnInfo.MachineKey,
				}).Info("set machine info in chain contract success with hash ", hash)

				ctx3, cancel3 := context.WithTimeout(ctx, 10*time.Second)
				defer cancel3()
				if err := db.MDB.SetMachineInfo(
					ctx3,
					wsConnInfo.MachineKey,
					miReq,
					calcPoint,
					longitude,
					latitude,
				); err != nil {
					log.Log.WithFields(logrus.Fields{
						"machine": wsConnInfo.MachineKey,
					}).Error("set machine info in database failed: ", err)
					writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
						WsHeader: types.WsHeader{
							Version:   0,
							Timestamp: time.Now().Unix(),
							Id:        req.Id,
							Type:      req.Type,
							PubKey:    []byte(""),
							Sign:      []byte(""),
						},
						Code:    uint32(types.ErrCodeDbcChain),
						Message: fmt.Sprintf("set machine info in database failed %v", err),
						Body:    []byte(""),
					})
					return nil
				} else {
					log.Log.WithFields(logrus.Fields{
						"machine": wsConnInfo.MachineKey,
					}).Info("set machine info in database success")
				}
			}
		}
	} else {
		log.Log.WithFields(logrus.Fields{
			"machine": wsConnInfo.MachineKey,
		}).WithField("machine info", miReq).Info("get machine info success and calcpoint ", mi.CalcPoint)
	}

	ctx2, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	if err := db.MDB.AddMachineTM(
		ctx2,
		wsConnInfo.MachineKey,
		time.UnixMilli(req.Timestamp),
		miReq,
	); err != nil {
		log.Log.WithFields(logrus.Fields{
			"machine": wsConnInfo.MachineKey,
		}).Error("add machine tm in database failed ", err)
		writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
			WsHeader: types.WsHeader{
				Version:   0,
				Timestamp: time.Now().Unix(),
				Id:        req.Id,
				Type:      req.Type,
				PubKey:    []byte(""),
				Sign:      []byte(""),
			},
			Code:    uint32(types.ErrCodeDatabase),
			Message: "add machine tm in database failed",
			Body:    []byte(""),
		})
		return nil
	}

	// pm.SetMetrics(nodeId, miReq)
	log.Log.WithFields(logrus.Fields{
		"machine": wsConnInfo.MachineKey,
	}).WithField("machine info", miReq).Info("add machine tm in database success")
	writeWsResponse(c, wsConnInfo.MachineKey, &types.WsResponse{
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
