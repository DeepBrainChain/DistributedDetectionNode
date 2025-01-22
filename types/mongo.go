package types

import "time"

type MDBMachineOnline struct {
	MachineKey
	AddTime time.Time `json:"add_time" bson:"add_time"`
}

type MDBMetaField struct {
	MachineKey
}

type MDBMachineInfo struct {
	Timestamp time.Time    `json:"timestamp" bson:"timestamp"`
	Machine   MDBMetaField `json:"machine" bson:"machine"`
	GPUNames  []string     `json:"gpu_names" bson:"gpu_names"`
	// UtilizationGPU int          `json:"utilization_gpu" bson:"utilization_gpu"`
	MemoryTotal int64 `json:"memory_total" bson:"memory_total"`
	// MemoryUsed     int64        `json:"memory_used" bson:"memory_used"`
}
