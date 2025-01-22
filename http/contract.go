package http

import (
	ohttp "net/http"

	"github.com/gin-gonic/gin"

	"DistributedDetectionNode/dbc"
	"DistributedDetectionNode/types"
)

func RegisterMachine(ctx *gin.Context, chain *dbc.DbcChain) {
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

	if err := chain.Report(types.MachineRegister, req.StakingType, req.ProjectName, req.MachineId); err != nil {
		rsp.Code = int(types.ErrCodeDbcChain)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusInternalServerError, rsp)
		return
	}
	ctx.JSON(ohttp.StatusOK, rsp)
}

func UnregisterMachine(ctx *gin.Context, chain *dbc.DbcChain) {
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

	if err := chain.Report(types.MachineUnregister, req.StakingType, req.ProjectName, req.MachineId); err != nil {
		rsp.Code = int(types.ErrCodeDbcChain)
		rsp.Message = err.Error()
		ctx.JSON(ohttp.StatusInternalServerError, rsp)
		return
	}
	ctx.JSON(ohttp.StatusOK, rsp)
}

func OnlineMachine(ctx *gin.Context, chain *dbc.DbcChain) {
	//
}

func OfflineMachine(ctx *gin.Context, chain *dbc.DbcChain) {
	//
}
