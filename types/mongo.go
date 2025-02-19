package types

import "time"

type MDBMachineOnline struct {
	MachineKey `bson:",inline"`
	AddTime    time.Time `json:"add_time" bson:"add_time"`
}

type MDBMachineInfo struct {
	MachineKey      `bson:",inline"`
	MachineInfo     `bson:",inline"`
	CalcPoint       float64   `json:"calc_point" bson:"calc_point"`
	RegisterTime    time.Time `json:"register_time" bson:"register_time"`
	LastOfflineTime time.Time `json:"last_offline_time" bson:"last_offline_time"`
}

type MDBMachineTM struct {
	Timestamp   time.Time  `json:"timestamp" bson:"timestamp"`
	Machine     MachineKey `json:"machine" bson:"machine"`
	MachineInfo `bson:",inline"`
}
