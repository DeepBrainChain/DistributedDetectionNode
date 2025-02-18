package types

import "time"

type MDBMachineOnline struct {
	MachineKey
	AddTime time.Time `json:"add_time" bson:"add_time"`
}

type MDBMachineInfo struct {
	MachineKey
	MachineInfo
	CalcPoint       float64   `json:"calc_point" bson:"calc_point"`
	RegisterTime    time.Time `json:"register_time" bson:"register_time"`
	LastOfflineTime time.Time `json:"last_offline_time" bson:"last_offline_time"`
}

type MDBMetaField struct {
	MachineKey
}

type MDBMachineTM struct {
	Timestamp time.Time    `json:"timestamp" bson:"timestamp"`
	Machine   MDBMetaField `json:"machine" bson:"machine"`
	MachineInfo
}
