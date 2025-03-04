package types

import "time"

type MDBMachineOnline struct {
	MachineKey `bson:",inline"`
	AddTime    time.Time `json:"add_time" bson:"add_time"`
}

type MDBMachineInfo struct {
	MachineKey      `bson:",inline"`
	MachineInfo     `bson:",inline"`
	StakingType     uint8     `json:"staking_type"`
	CalcPoint       float64   `json:"calc_point" bson:"calc_point"`
	Longitude       float32   `json:"longitude" bson:"longitude"`
	Latitude        float32   `json:"latitude" bson:"latitude"`
	RegisterTime    time.Time `json:"register_time" bson:"register_time"`
	LastOfflineTime time.Time `json:"last_offline_time" bson:"last_offline_time"`
	// LastRegisterTime time.Time `json:"last_register_time" bson:"last_register_time"`
}

type MDBMachineTM struct {
	Timestamp   time.Time  `json:"timestamp" bson:"timestamp"`
	Machine     MachineKey `json:"machine" bson:"machine"`
	MachineInfo `bson:",inline"`
}
