package types

type OfflineNotifyRequest struct {
	MachineId string `json:"machine_id"`
}

type OfflineNotifyResponse struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Success bool   `json:"success"`
}
