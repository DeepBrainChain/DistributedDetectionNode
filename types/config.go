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

type Prometheus struct {
	JobName        string `json:"JobName"`
	RemoteWriteURL string `json:"RemoteWriteURL"`
}

type Config struct {
	Addr       string     `json:"Addr"`
	LogLevel   string     `json:"LogLevel"`
	LogFile    string     `json:"LogFile"`
	MongoDB    MongoDB    `json:"MongoDB"`
	Prometheus Prometheus `json:"Prometheus"`
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
