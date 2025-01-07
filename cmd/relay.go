// Copyright 2021 stafiprotocol
// SPDX-License-Identifier: LGPL-3.0-only

package cmd

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/stafiprotocol/chainbridge/utils/crypto/secp256k1"
	"github.com/stafiprotocol/chainbridge/utils/keystore"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/config"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/log"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/utils"
	"github.com/stafiprotocol/eth-lsd-relay/service"
)

func startRelayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start lsd relay",
		RunE: func(cmd *cobra.Command, args []string) error {
			basePath, err := cmd.Flags().GetString(flagBasePath)
			if err != nil {
				return err
			}
			cfg, err := config.Load(basePath)
			if err != nil {
				return err
			}
			fmt.Printf("keystore path: %s\n", cfg.KeystorePath)

			logLevelStr, err := cmd.Flags().GetString(flagLogLevel)
			if err != nil {
				return err
			}
			logLevel, err := logrus.ParseLevel(logLevelStr)
			if err != nil {
				return err
			}
			logrus.SetLevel(logLevel)

			// init constant variables
			utils.StandardEffectiveBalance = cfg.Eth2EffectiveBalance * 1e9                                                        // unit Gwei
			utils.StandardEffectiveBalanceDeci = decimal.NewFromInt(int64(utils.StandardEffectiveBalance)).Mul(utils.GweiDeci)     // unit wei
			utils.MaxPartialWithdrawalAmount = cfg.MaxPartialWithdrawalAmount * 1e9                                                // unit Gwei
			utils.MaxPartialWithdrawalAmountDeci = decimal.NewFromInt(int64(utils.MaxPartialWithdrawalAmount)).Mul(utils.GweiDeci) // unit wei

			logrus.Infof(
				`config info:
  logFilePath: %s
  logLevel: %s
  account: %s
  runForEntrustedLsdNetwork: %v
  lsdTokenAddress: %s
  factoryAddress: %s
  batchRequestBlocksNumber: %d
  maxGasPrice: %s Gwei
  endpoints: %v`,
				cfg.LogFilePath, logLevelStr, cfg.Account,
				cfg.RunForEntrustedLsdNetwork, cfg.Contracts.LsdTokenAddress, cfg.Contracts.LsdFactoryAddress,
				cfg.BatchRequestBlocksNumber, cfg.MaxGasPrice, cfg.Endpoints)

			err = log.InitLogFile(cfg.LogFilePath + "/relay")
			if err != nil {
				return fmt.Errorf("InitLogFile failed: %w", err)
			}

			//interrupt signal
			ctx := utils.ShutdownListener()

			// load voter account
			kpI, err := keystore.KeypairFromAddress(strings.ToLower(cfg.Account), keystore.EthChain, cfg.KeystorePath, false)
			if err != nil {
				return err
			}
			kp, ok := kpI.(*secp256k1.Keypair)
			if !ok {
				return fmt.Errorf(" keypair err")
			}
			srvManager, err := service.NewServiceManager(cfg, kp)
			if err != nil {
				return fmt.Errorf("NewServiceManager err: %w", err)
			}
			if err = srvManager.Start(); err != nil {
				logrus.Errorf("start service manager err: %s", utils.ErrToLogStr(err))
				return err
			}

			defer func() {
				logrus.Infof("shutting down task ...")
				srvManager.Stop()
			}()

			<-ctx.Done()
			return nil
		},
	}

	cmd.Flags().String(flagBasePath, defaultBasePath, "base path a directory where your config.toml resids")
	cmd.Flags().String(flagLogLevel, logrus.InfoLevel.String(), "The logging level (trace|debug|info|warn|error|fatal|panic)")

	return cmd
}
