package types

import (
	"encoding/json"
	"os"
)

type MongoDB struct {
	URI        string `json:"URI"`
	Database   string `json:"Database"`
	ExpireTime int64  `json:"ExpireTime"`
}

type IP2LocationDB struct {
	DatabasePath string `json:"DatabasePath"`
}

type Prometheus struct {
	JobName        string `json:"JobName"`
	RemoteWriteURL string `json:"RemoteWriteURL"`
}

type ContractConfig struct {
	AbiFile         string `json:"AbiFile"`
	ContractAddress string `json:"ContractAddress"`
}

type ChainConfig struct {
	Rpc                 string         `json:"Rpc"`
	RpcEndpoints        []string       `json:"RpcEndpoints,omitempty"` // multiple RPCs for failover
	ChainId             int64          `json:"ChainId"`
	PrivateKey          string         `json:"PrivateKey"`             // single key (backward compat)
	PrivateKeys         []string       `json:"PrivateKeys,omitempty"`  // multiple keys for parallel tx
	ReportContract      ContractConfig `json:"ReportContract"`
	MachineInfoContract ContractConfig `json:"MachineInfoContract"`
	RentContract        ContractConfig `json:"RentContract,omitempty"`        // Rent 合约，用于查询 isRented 状态
	FreeRentalContract  ContractConfig `json:"FreeRentalContract,omitempty"`  // FreeRental 合约（可选）
}

type Certificate struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

type NotifyThirdParty struct {
	OfflineNotify string `json:"OfflineNotify"`
}

type Config struct {
	Addr             string           `json:"Addr"`
	LogLevel         string           `json:"LogLevel"`
	LogFile          string           `json:"LogFile"`
	MongoDB          MongoDB          `json:"MongoDB"`
	IP2LDB           IP2LocationDB    `json:"IP2LDB"`
	Prometheus       Prometheus       `json:"Prometheus"`
	Chain            ChainConfig      `json:"Chain"`
	Certificate      Certificate      `json:"Certificate"`
	NotifyThirdParty NotifyThirdParty `json:"NotifyThirdParty"`
	// InternalSecret 保护 /api/v0/contract/* 端点（链上 register/unregister/online/offline 退租惩罚）。
	// 该端口同时对外提供 /websocket（矿机连接），无法整端口防火墙，故对敏感的合约组做共享密钥认证。
	// 可被环境变量 DDN_INTERNAL_SECRET 覆盖。留空则拒绝所有 /contract 请求（fail-closed）。
	InternalSecret string `json:"InternalSecret,omitempty"`
}

func LoadConfig(configPath string) (*Config, error) {
	configFile, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	config := &Config{}
	err = json.Unmarshal(configFile, &config)
	if err != nil {
		return nil, err
	}
	return config, nil
}
