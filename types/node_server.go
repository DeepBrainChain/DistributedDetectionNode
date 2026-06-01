package types

type OfflineNotifyRequest struct {
	MachineId    string `json:"machine_id"`
	IsOnline     bool   `json:"is_online"`
	ReportTxHash string `json:"report_tx_hash,omitempty"`
	// CheckOnly: 链上 Report 前的只读预检。后端只返回在线判断、不执行惩罚/订单终止(零副作用)。
	CheckOnly bool `json:"check_only,omitempty"`
}

type OfflineNotifyResponse struct {
	Code     int    `json:"code"`
	Msg      string `json:"msg"`
	Success  bool   `json:"success"`
	IsRented bool   `json:"is_rented"`
	// FalseOfflineGuard: 后端判定机器实际在线+SDK正常(检测节点误判)。true → 应跳过链上 Report 防误强制退租。
	FalseOfflineGuard bool `json:"false_offline_guard"`
}
