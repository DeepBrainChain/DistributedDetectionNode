package types

import "time"

// machine connection table
type MDBMachineOnline struct {
	MachineKey `bson:",inline"`
	AddTime    time.Time `json:"add_time" bson:"add_time"`
	// [FIX 2026-07-28] ConnId = 建立本条记录的那个 WS 连接身份(client UUID)。
	// 用途：机器重启重连要"替换"旧连接时，让 MachineDisconnected 只删「自己那条」记录
	// (conn_id 匹配)，避免旧连接的异步清理误删新连接刚写入的记录。旧记录无此字段=空串，
	// 兼容（空 conn_id 的老 doc 仍可被 upsert 覆盖）。
	ConnId string `json:"conn_id" bson:"conn_id"`
}

// machine info table
type MDBMachineInfo struct {
	MachineKey `bson:",inline"`
	// MachineInfo        `bson:",inline"`
	StakingType        uint8     `json:"staking_type"`
	RegisterTime       time.Time `json:"register_time" bson:"register_time"`
	LastOfflineTime    time.Time `json:"last_offline_time" bson:"last_offline_time"`
	LastDisconnectTime time.Time `json:"last_disconnect_time" bson:"last_disconnect_time"`
	// LastRegisterTime time.Time `json:"last_register_time" bson:"last_register_time"`
	MDBDeepLinkMachineInfoST        `json:"deeplink_st" bson:"deeplink_st,omitempty"`
	MDBDeepLinkMachineInfoBandwidth `json:"deeplink_bw" bson:"deeplink_bw,omitempty"`
}

// time series table
type MDBMachineTM struct {
	Timestamp time.Time  `json:"timestamp" bson:"timestamp"`
	Machine   MachineKey `json:"machine" bson:"machine"`
	// MachineInfo `bson:",inline"`
	DeepLinkMachineInfoST        `json:"deeplink_st" bson:"deeplink_st,omitempty"`
	DeepLinkMachineInfoBandwidth `json:"deeplink_bw" bson:"deeplink_bw,omitempty"`
}

// machine info of deeplink short-term
type MDBDeepLinkMachineInfoST struct {
	DeepLinkMachineInfoST `bson:",inline"`
	CalcPoint             float64 `json:"calc_point" bson:"calc_point,omitempty"`
	Longitude             float32 `json:"longitude" bson:"longitude,omitempty"`
	Latitude              float32 `json:"latitude" bson:"latitude,omitempty"`
	Region                string  `json:"region" bson:"region,omitempty"`
}

// machine info of deeplink bandwidth
type MDBDeepLinkMachineInfoBandwidth struct {
	DeepLinkMachineInfoBandwidth `bson:",inline"`
	Region                       string `json:"region" bson:"region,omitempty"`
}
