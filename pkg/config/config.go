// Copyright 2021 stafiprotocol
// SPDX-License-Identifier: LGPL-3.0-only

package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Eth1Endpoint             string // url for eth1 rpc endpoint
	Eth2Endpoint             string // url for eth2 rpc endpoint
	StorageApiToken          string //
	LogFilePath              string
	Account                  string
	KeystorePath             string
	BlockstoreFilePath       string
	GasLimit                 string
	MaxGasPrice              string
	BatchRequestBlocksNumber uint64

	RunForEntrustedLsdNetwork bool

	Contracts Contracts
}

type Contracts struct {
	LsdTokenAddress   string
	LsdFactoryAddress string
}

func Load(basePath string) (*Config, error) {
	basePath = strings.TrimSuffix(basePath, "/")
	configFilePath := basePath + "/config.toml"
	fmt.Printf("config path: %s\n", configFilePath)

	var cfg = Config{}
	if err := loadSysConfig(configFilePath, &cfg); err != nil {
		return nil, err
	}
	cfg.LogFilePath = basePath + "/log_data"
	cfg.KeystorePath = KeyStoreFilePath(basePath)
	cfg.BlockstoreFilePath = basePath + "/blockstore"

	return &cfg, nil
}

func KeyStoreFilePath(basePath string) string {
	basePath = strings.TrimSuffix(basePath, "/")
	return basePath + "/keystore"
}

func loadSysConfig(path string, config *Config) error {
	_, err := os.Open(path)
	if err != nil {
		return err
	}
	if _, err := toml.DecodeFile(path, config); err != nil {
		return err
	}
	fmt.Println("load config success")
	return nil
}
