package types

import "time"

type MDBDeviceOnline struct {
	DeviceId string    `json:"device_id" bson:"device_id"`
	AddTime  time.Time `json:"add_time" bson:"add_time"`
}

type MDBMetaField struct {
	DeviceId string      `json:"device_id" bson:"device_id"`
	Project  string      `json:"project" bson:"project"`
	Models   []ModelInfo `json:"models" bson:"models"`
	GPUName  string      `json:"gpu_name" bson:"gpu_name"`
}

type MDBDeviceInfo struct {
	Timestamp      time.Time    `json:"timestamp" bson:"timestamp"`
	Device         MDBMetaField `json:"device" bson:"device"`
	UtilizationGPU int          `json:"utilization_gpu" bson:"utilization_gpu"`
	MemoryTotal    int64        `json:"memory_total" bson:"memory_total"`
	MemoryUsed     int64        `json:"memory_used" bson:"memory_used"`
}
