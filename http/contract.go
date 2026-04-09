package http

import (
	"context"
	"errors"
	ohttp "net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/mongo"

	"DistributedDetectionNode/db"
	"DistributedDetectionNode/dbc"
	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"
)

var ReportContractTimeout = 60 * time.Second

func RegisterMachine(ctx *gin.Context) {
	rsp := types.BaseHttpResponse{
		Code:    0,
		Message: "ok",
	}

	var req types.ContractReportInfo
	if err := ctx.ShouldBindJSON(&req); err != nil {
		rsp.Code = int(types.ErrCodeParse)
		rsp.Message = types.ErrCodeParse.String()
		ctx.JSON(ohttp.StatusBadRequest, rsp)
		return
	}
	if err := req.Validate(); err != nil {
		rsp.Code = int(types.ErrCodeParam)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusBadRequest, rsp)
		return
	}

	ctx1, cancel := context.WithTimeout(ctx.Request.Context(), ReportContractTimeout)
	defer cancel()

	hash, err := dbc.DbcChain.Report(ctx1, types.MachineRegister, req.StakingType, req.ProjectName, req.MachineId)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": req}).Errorf("machine register failed: %v with hash %v", err, hash)
		rsp.Code = int(types.ErrCodeDbcChain)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusInternalServerError, rsp)
		return
	}
	log.Log.WithFields(logrus.Fields{"machine": req}).Info("machine register success with hash ", hash)

	mi, err := db.MDB.GetMachineInfo(
		ctx1,
		types.MachineKey{
			MachineId:   req.MachineId,
			Project:     req.ProjectName,
			ContainerId: req.ContainerId,
		},
	)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			if err := db.MDB.RegisterMachine(
				ctx1,
				types.MachineKey{
					MachineId:   req.MachineId,
					Project:     req.ProjectName,
					ContainerId: req.ContainerId,
				},
				req.StakingType,
			); err != nil {
				log.Log.WithFields(logrus.Fields{"machine": req}).Errorf("machine register failed: %v when insert database", err)
				rsp.Code = int(types.ErrCodeDatabase)
				rsp.Message = err.Error()
				ctx.JSON(ohttp.StatusInternalServerError, rsp)
				return
			}
		} else {
			log.Log.WithFields(logrus.Fields{"machine": req}).Errorf("machine register failed: %v when query database", err)
			rsp.Code = int(types.ErrCodeDatabase)
			rsp.Message = err.Error()
			ctx.JSON(ohttp.StatusInternalServerError, rsp)
			return
		}
	} else if !mi.RegisterTime.IsZero() {
		rsp.Message = "already registed"
	}

	ctx.JSON(ohttp.StatusOK, rsp)
}

func UnregisterMachine(ctx *gin.Context, unregisterNotify func(machine types.MachineKey, stakingType types.StakingType)) {
	rsp := types.BaseHttpResponse{
		Code:    0,
		Message: "ok",
	}

	var req types.ContractReportInfo
	if err := ctx.ShouldBindJSON(&req); err != nil {
		rsp.Code = int(types.ErrCodeParse)
		rsp.Message = types.ErrCodeParse.String()
		ctx.JSON(ohttp.StatusBadRequest, rsp)
		return
	}
	if err := req.Validate(); err != nil {
		rsp.Code = int(types.ErrCodeParam)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusBadRequest, rsp)
		return
	}

	ctx1, cancel := context.WithTimeout(ctx.Request.Context(), ReportContractTimeout)
	defer cancel()

	hash, err := dbc.DbcChain.Report(ctx1, types.MachineUnregister, req.StakingType, req.ProjectName, req.MachineId)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": req}).Errorf("machine unregister failed: %v with hash %v", err, hash)
		rsp.Code = int(types.ErrCodeDbcChain)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusInternalServerError, rsp)
		return
	}
	log.Log.WithFields(logrus.Fields{"machine": req}).Info("machine unregister success with hash ", hash)

	unregisterNotify(
		types.MachineKey{
			MachineId:   req.MachineId,
			Project:     req.ProjectName,
			ContainerId: req.ContainerId,
		},
		req.StakingType,
	)

	if err := db.MDB.UnregisterMachine(ctx1, types.MachineKey{
		MachineId:   req.MachineId,
		Project:     req.ProjectName,
		ContainerId: req.ContainerId,
	}); err != nil {
		log.Log.WithFields(logrus.Fields{"machine": req}).Errorf("machine unregister failed: %v when delete database", err)
		rsp.Code = int(types.ErrCodeDatabase)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusInternalServerError, rsp)
		return
	}
	ctx.JSON(ohttp.StatusOK, rsp)
}

func OnlineMachine(ctx *gin.Context) {
	rsp := types.BaseHttpResponse{
		Code:    0,
		Message: "ok",
	}

	var req types.ContractReportInfo
	if err := ctx.ShouldBindJSON(&req); err != nil {
		rsp.Code = int(types.ErrCodeParse)
		rsp.Message = types.ErrCodeParse.String()
		ctx.JSON(ohttp.StatusBadRequest, rsp)
		return
	}
	if err := req.Validate(); err != nil {
		rsp.Code = int(types.ErrCodeParam)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusBadRequest, rsp)
		return
	}

	ctx1, cancel := context.WithTimeout(ctx.Request.Context(), ReportContractTimeout)
	defer cancel()

	hash, err := dbc.DbcChain.Report(ctx1, types.MachineOnline, req.StakingType, req.ProjectName, req.MachineId)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": req}).Errorf("machine online failed: %v with hash %v", err, hash)
		rsp.Code = int(types.ErrCodeDbcChain)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusInternalServerError, rsp)
		return
	}
	log.Log.WithFields(logrus.Fields{"machine": req}).Info("machine online success with hash ", hash)
	ctx.JSON(ohttp.StatusOK, rsp)
}

func OfflineMachine(ctx *gin.Context) {
	rsp := types.BaseHttpResponse{
		Code:    0,
		Message: "ok",
	}

	var req types.ContractReportInfo
	if err := ctx.ShouldBindJSON(&req); err != nil {
		rsp.Code = int(types.ErrCodeParse)
		rsp.Message = types.ErrCodeParse.String()
		ctx.JSON(ohttp.StatusBadRequest, rsp)
		return
	}
	if err := req.Validate(); err != nil {
		rsp.Code = int(types.ErrCodeParam)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusBadRequest, rsp)
		return
	}

	ctx1, cancel := context.WithTimeout(ctx.Request.Context(), ReportContractTimeout)
	defer cancel()

	// Check if this is a FreeRental machine
	if dbc.DbcChain.FreeRentalEnabled() {
		isFreeRental, err := dbc.DbcChain.IsFreeRentalMachine(ctx1, req.MachineId)
		if err != nil {
			// RPC 失败时跳过惩罚（FreeRental 机器走 staked Report 会 revert）
			log.Log.WithFields(logrus.Fields{"machine": req}).Warnf("IsFreeRentalMachine RPC failed: %v, skipping penalty", err)
			ctx.JSON(ohttp.StatusOK, rsp)
			return
		}
		if isFreeRental {
			// For FreeRental machines, only penalize if rented
			isRented, err := dbc.DbcChain.IsFreeRentalRented(ctx1, req.MachineId)
			if err != nil {
				log.Log.WithFields(logrus.Fields{"machine": req}).Warnf("[FreeRental] IsFreeRentalRented RPC failed, skipping penalty (safety): %v", err)
				// 安全策略：RPC 失败时跳过惩罚，避免误调质押合约 Report
				ctx.JSON(ohttp.StatusOK, rsp)
				return
			} else if !isRented {
				log.Log.WithFields(logrus.Fields{"machine": req}).Info("FreeRental machine offline but not rented, skipping penalty")
				ctx.JSON(ohttp.StatusOK, rsp)
				return
			} else {
				// Rented FreeRental machine — call FreeRental.notify(4, machineId)
				hash, err := dbc.DbcChain.NotifyFreeRental(ctx1, 4, req.MachineId)
				if err != nil {
					log.Log.WithFields(logrus.Fields{"machine": req}).Errorf("FreeRental notify offline failed: %v with hash %v", err, hash)
					rsp.Code = int(types.ErrCodeDbcChain)
					rsp.Message = err.Error()
					ctx.JSON(ohttp.StatusInternalServerError, rsp)
					return
				}
				log.Log.WithFields(logrus.Fields{"machine": req}).Infof("FreeRental notify offline success with hash %v", hash)
				ctx.JSON(ohttp.StatusOK, rsp)
				return
			}
		}
	}

	hash, err := dbc.DbcChain.Report(ctx1, types.MachineOffline, req.StakingType, req.ProjectName, req.MachineId)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": req}).Errorf("machine offline failed: %v with hash %v", err, hash)
		rsp.Code = int(types.ErrCodeDbcChain)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusInternalServerError, rsp)
		return
	}
	log.Log.WithFields(logrus.Fields{"machine": req}).Info("machine offline success with hash ", hash)
	ctx.JSON(ohttp.StatusOK, rsp)
}
