package types

type MachineKey struct {
	MachineId   string `json:"machine_id" bson:"machine_id"`
	Project     string `json:"project" bson:"project"`
	ContainerId string `json:"container_id" bson:"container_id"`
}

type MachineInfo struct {
	// Project string `json:"project" bson:"project"`
	// Models         []ModelInfo `json:"models" bson:"models"`
	GPUNames       []string `json:"gpu_names" bson:"gpu_names"`
	GPUMemoryTotal []int32  `json:"gpu_memory_total" bson:"gpu_memory_total"` // 只有像 4060 Ti 这种显存有两个版本的情况才使用此字段
	// UtilizationGPU int      `json:"utilization_gpu" bson:"utilization_gpu"` // GPU 使用率，乘以 100 取整
	MemoryTotal int64 `json:"memory_total" bson:"memory_total"` // 内存总大小，单位 字节
	// MemoryUsed     int64    `json:"memory_used" bson:"memory_used"`         // 已用内存，单位 字节
	CpuType  string `json:"cpu_type" bson:"cpu_type"` // CPU 类型
	CpuRate  int32  `json:"cpu_rate" bson:"cpu_rate"` // CPU 频率
	Wallet   string `json:"wallet" bson:"wallet"`     // 设备所有者的钱包地址
	ClientIP string `json:"client_ip" bson:"client_ip"`
}
