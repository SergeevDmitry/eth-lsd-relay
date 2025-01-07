package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/stafiprotocol/chainbridge/utils/crypto"
	"github.com/stafiprotocol/chainbridge/utils/crypto/secp256k1"
	"github.com/stafiprotocol/chainbridge/utils/keystore"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/config"
)

func importAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import-account",
		Args:  cobra.ExactArgs(0),
		Short: "Import account",
		RunE: func(cmd *cobra.Command, args []string) error {
			basePath, err := cmd.Flags().GetString(flagBasePath)
			if err != nil {
				return err
			}

			logLevelStr, err := cmd.Flags().GetString(flagLogLevel)
			if err != nil {
				return err
			}
			logLevel, err := logrus.ParseLevel(logLevelStr)
			if err != nil {
				return err
			}
			logrus.SetLevel(logLevel)

			keystorePath := config.KeyStoreFilePath(basePath)
			fmt.Printf("keystore path: %s\n", keystorePath)

			return generateKeyFileByPrivateKey(keystorePath)
		},
	}
	cmd.Flags().String(flagBasePath, defaultBasePath, "base path a directory where your config.toml resids")
	cmd.Flags().String(flagLogLevel, logrus.InfoLevel.String(), "The logging level (trace|debug|info|warn|error|fatal|panic)")
	return cmd
}

func generateKeyFileByPrivateKey(keypath string) error {
	var kp crypto.Keypair
	var err error

	key := keystore.GetPassword("Enter private key:")
	skey := string(key)

	if skey[0:2] == "0x" {
		kp, err = secp256k1.NewKeypairFromString(skey[2:])
	} else {
		kp, err = secp256k1.NewKeypairFromString(skey)
	}
	if err != nil {
		return fmt.Errorf("could not generate secp256k1 keypair from given string: %s", err)
	}

	fp, err := filepath.Abs(keypath + "/" + strings.ToLower(kp.Address()) + ".key")
	if err != nil {
		return fmt.Errorf("invalid filepath: %s", err)
	}

	if _, err := os.Stat(fp); err != nil {
		err := os.MkdirAll(filepath.Dir(fp), 0700)
		if err != nil {
			return err
		}
	}

	file, err := os.OpenFile(filepath.Clean(fp), os.O_EXCL|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	defer func() {
		err = file.Close()
		if err != nil {
			logrus.Error("generate keypair: could not close keystore file")
		}
	}()

	password := keystore.GetPassword("password for key:")
	err = keystore.EncryptAndWriteToFile(file, kp, password)
	if err != nil {
		return fmt.Errorf("could not write key to file: %s", err)
	}
	logrus.WithFields(logrus.Fields{
		"address": kp.Address(),
		"file":    fp,
	}).Info("key imported")

	return nil
}
