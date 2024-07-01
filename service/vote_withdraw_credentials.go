package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/prysmaticlabs/prysm/v4/contracts/deposit"
	ethpb "github.com/prysmaticlabs/prysm/v4/proto/prysm/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/connection/types"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/utils"
)

func (s *Service) voteWithdrawCredentials() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	validatorListNeedVote := make([]*Validator, 0, len(s.validators))
	for _, val := range s.validators {
		if !(val.Status == utils.ValidatorStatusDeposited || val.Status == utils.ValidatorStatusWithdrawUnmatch) {
			continue
		}

		validatorListNeedVote = append(validatorListNeedVote, val)
	}
	validatorPubkeys := make([][]byte, 0)
	validatorMatches := make([]bool, 0)
	for _, validator := range validatorListNeedVote {
		// skip if not sync to deposit block
		if validator.DepositBlock > s.latestBlockOfSyncEvents {
			continue
		}

		govCredentials := s.govDeposits[hex.EncodeToString(validator.Pubkey)]

		match := true
		for _, l := range govCredentials {
			if !bytes.Equal(s.withdrawCredentials, l) {
				match = false
			}
		}

		validatorPubkey := types.BytesToValidatorPubkey(validator.Pubkey)
		validatorStatus, err := s.connection.GetValidatorStatus(ctx, validatorPubkey, nil)
		if err != nil {
			return err
		}

		s.log.WithFields(logrus.Fields{
			"status": validatorStatus,
		}).Debug("validator beacon status")

		if validatorStatus.Exists && bytes.Equal(validatorStatus.WithdrawalCredentials[:], s.withdrawCredentials) {
			match = false

			s.log.WithFields(logrus.Fields{
				"validatorStatus.WithdrawalCredentials": validatorStatus.WithdrawalCredentials.String(),
				"task.withdrawCredentials":              hex.EncodeToString(s.withdrawCredentials),
			}).Warn("withdrawalCredentials not match")
		}

		govDepositAmount := uint64(1e9)
		if validator.NodeType == utils.NodeTypeSolo {
			govDepositAmount = validator.NodeDepositAmountDeci.Div(utils.GweiDeci).BigInt().Uint64()
		}

		dp := ethpb.Deposit_Data{
			PublicKey:             validatorPubkey.Bytes(),
			WithdrawalCredentials: s.withdrawCredentials,
			Amount:                govDepositAmount,
			Signature:             validator.DepositSignature,
		}

		if err := deposit.VerifyDepositSignature(&dp, s.domain); err != nil {
			match = false

			s.log.WithFields(logrus.Fields{
				"task.withdrawCredentials":              s.withdrawCredentials,
				"validatorStatus.WithdrawalCredentials": validatorStatus.WithdrawalCredentials.String(),
			}).Warn("signature not match")
		}

		s.log.WithFields(logrus.Fields{
			"pubkey": validator.Pubkey,
			"match":  match,
		}).Debug("match info")

		validatorPubkeys = append(validatorPubkeys, validator.Pubkey)
		validatorMatches = append(validatorMatches, match)
	}

	return s.voteWithdrawCredentialsTx(validatorPubkeys, validatorMatches)
}

func (s *Service) voteWithdrawCredentialsTx(validatorPubkeys [][]byte, matches []bool) error {
	if len(validatorPubkeys) == 0 {
		return nil
	}
	if len(validatorPubkeys) != len(matches) {
		return fmt.Errorf("validators and matches len not match")
	}

	tos := make([]common.Address, 0)
	callDatas := make([][]byte, 0)
	blocks := make([]*big.Int, 0)
	proposalIds := make([][32]byte, 0)

	for i := 0; i < len(validatorPubkeys); i++ {

		encodeBts, err := s.nodeDepositAbi.Pack("voteWithdrawCredentials", validatorPubkeys[i], matches[i])
		if err != nil {
			return err
		}

		proposalId := utils.ProposalId(s.nodeDepositAddress, encodeBts, big.NewInt(0))

		// check voted
		hasVoted, err := s.networkProposalContract.HasVoted(nil, proposalId, s.connection.Keypair().CommonAddress())
		if err != nil {
			return fmt.Errorf("networkProposalContract.HasVoted err: %s", err)
		}
		if hasVoted {
			continue
		}

		tos = append(tos, s.nodeDepositAddress)
		callDatas = append(callDatas, encodeBts)
		blocks = append(blocks, big.NewInt(0))
		proposalIds = append(proposalIds, proposalId)
	}

	if len(tos) == 0 {
		return nil
	}

	err := s.connection.LockAndUpdateTxOpts()
	if err != nil {
		return err
	}
	defer s.connection.UnlockTxOpts()

	s.log.WithFields(logrus.Fields{
		"gasPrice": s.connection.TxOpts().GasPrice.String(),
		"gasLimit": s.connection.TxOpts().GasLimit,
	}).Debug("tx opts")

	s.log.WithFields(logrus.Fields{
		"pubkeys": pubkeyToHex(validatorPubkeys),
		"matches": matches,
	}).Info("voteForNode")

	tx, err := s.networkProposalContract.BatchExecProposals(s.connection.TxOpts(), tos, callDatas, blocks)
	if err != nil {
		return err
	}

	s.log.Info("send vote tx hash: ", tx.Hash().String())

	return s.waitProposalsTxOk(tx.Hash(), proposalIds)
}

func pubkeyToHex(pubkeys [][]byte) []string {
	ret := make([]string, len(pubkeys))
	for i, pubkey := range pubkeys {
		ret[i] = hex.EncodeToString(pubkey)
	}
	return ret
}
