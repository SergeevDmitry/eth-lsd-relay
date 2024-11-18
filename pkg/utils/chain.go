package utils

import (
	"math"

	"github.com/stratisproject/prysm-stratis/config/params"
)

// PulseChainConfig defines the config for the PulseChain beacon chain mainnet.
func PulseChainConfig() *params.BeaconChainConfig {
	const PulseChainName = "pulsechain"
	cfg := params.MainnetConfig().Copy()
	cfg.ConfigName = PulseChainName
	cfg.PresetBase = "pulsechain"

	// preset overrides
	cfg.BaseRewardFactor = 64000
	cfg.EffectiveBalanceIncrement = 1 * 1e15
	cfg.MaxEffectiveBalance = 32 * 1e15

	// config overrides
	cfg.TerminalTotalDifficulty = "58750003716598352947541"
	cfg.MinGenesisActiveValidatorCount = 4096
	cfg.MinGenesisTime = 1683776400
	cfg.GenesisForkVersion = []byte{0x00, 0x00, 0x03, 0x69}
	cfg.GenesisDelay = 300
	cfg.AltairForkVersion = []byte{0x00, 0x00, 0x03, 0x6a}
	cfg.AltairForkEpoch = 1
	cfg.BellatrixForkVersion = []byte{0x00, 0x00, 0x03, 0x6b}
	cfg.BellatrixForkEpoch = 2
	cfg.CapellaForkVersion = []byte{0x00, 0x00, 0x03, 0x6c}
	cfg.CapellaForkEpoch = 3
	cfg.DenebForkVersion = []byte{0x00, 0x00, 0x03, 0x6d}
	cfg.DenebForkEpoch = math.MaxUint64
	cfg.SecondsPerSlot = 10
	cfg.EjectionBalance = 16 * 1e15
	cfg.DepositChainID = 369
	cfg.DepositNetworkID = 369
	cfg.DepositContractAddress = "0x3693693693693693693693693693693693693693"

	cfg.InitializeForkSchedule()
	return cfg
}

const PulseChainTestnetV4Name = "pulsechain-testnet-v4"

// PulseChainTestnetV4Config defines the config for the PulseChain beacon chain testnet.
func PulseChainTestnetV4Config() *params.BeaconChainConfig {
	cfg := params.MainnetConfig().Copy()
	cfg.ConfigName = PulseChainTestnetV4Name
	cfg.PresetBase = "pulsechain"

	// preset overrides
	cfg.BaseRewardFactor = 64000
	cfg.EffectiveBalanceIncrement = 1 * 1e15
	cfg.MaxEffectiveBalance = 32 * 1e15

	// config overrides
	cfg.TerminalTotalDifficulty = "58750003716598352947541"
	cfg.MinGenesisActiveValidatorCount = 4096
	cfg.MinGenesisTime = 1674864000
	cfg.GenesisForkVersion = []byte{0x00, 0x00, 0x09, 0x43}
	cfg.GenesisDelay = 300
	cfg.AltairForkVersion = []byte{0x00, 0x00, 0x09, 0x44}
	cfg.AltairForkEpoch = 1
	cfg.BellatrixForkVersion = []byte{0x00, 0x00, 0x09, 0x45}
	cfg.BellatrixForkEpoch = 2
	cfg.CapellaForkVersion = []byte{0x00, 0x00, 0x09, 0x46}
	cfg.CapellaForkEpoch = 4200
	cfg.DenebForkVersion = []byte{0x00, 0x00, 0x09, 0x47}
	cfg.DenebForkEpoch = math.MaxUint64
	cfg.SecondsPerSlot = 10
	cfg.EjectionBalance = 16 * 1e15
	cfg.DepositChainID = 943
	cfg.DepositNetworkID = 943
	cfg.DepositContractAddress = "0x3693693693693693693693693693693693693693"

	cfg.InitializeForkSchedule()
	return cfg
}
