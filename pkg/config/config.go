// Copyright 2021 stafiprotocol
// SPDX-License-Identifier: LGPL-3.0-only

package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Endpoint struct {
	Eth1 string
	Eth2 string
}

type Config struct {
	LogFilePath              string
	Account                  string
	KeystorePath             string
	BlockstoreFilePath       string
	GasLimit                 string
	MaxGasPrice              string
	BatchRequestBlocksNumber uint64

	RunForEntrustedLsdNetwork bool

	Contracts   Contracts
	Endpoints   []Endpoint
	Web3Storage Web3Storage
	Pinata      Pinata
}

type Web3Storage struct {
	PrivateKey string
	SpaceDid   string
	ProofFile  string
}

type Pinata struct {
	Jwt      string
	Endpoint string
	PinDays  uint
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
