package service

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/utils"
)

func (s *Service) notifyValidatorExit() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*5)
	defer cancel()
	currentCycle, targetTimestamp, err := s.currentCycleAndStartTimestamp()
	if err != nil {
		return fmt.Errorf("currentCycleAndStartTimestamp failed: %w", err)
	}
	willDealCycle := currentCycle - 1

	targetEpoch := utils.EpochAtTimestamp(s.eth2Config, uint64(targetTimestamp))
	targetBlockNumber, err := s.getEpochStartBlocknumberWithCheck(targetEpoch)
	if err != nil {
		return fmt.Errorf("getEpochStartBlocknumberWithCheck failed: %w", err)
	}
	if targetBlockNumber <= s.startAtBlock {
		return nil
	}

	// wait validator updated
	if targetEpoch > s.latestEpochOfUpdateValidator {
		s.log.Debugf("targetEpoch: %d  latestEpochOfUpdateValidator: %d", targetEpoch, s.latestEpochOfUpdateValidator)
		return nil
	}

	// wait sync block
	if targetBlockNumber > s.latestBlockOfSyncBlock {
		s.log.Debugf("targetBlockNumber: %d  latestBlockOfSyncBlock: %d", targetBlockNumber, s.latestBlockOfSyncBlock)
		return nil
	}

	targetCall := s.connection.CallOpts(big.NewInt(int64(targetBlockNumber)))

	totalMissingAmount, err := s.networkWithdrawContract.TotalMissingAmountForWithdraw(targetCall)
	if err != nil {
		return fmt.Errorf("TotalMissingAmountForWithdraw failed: %w, target block: %d", err, targetBlockNumber)
	}
	totalMissingAmountDeci := decimal.NewFromBigInt(totalMissingAmount, 0)

	// no need notify exit election
	if totalMissingAmount.Cmp(big.NewInt(0)) == 0 {
		return nil
	}

	ejectedValidator, err := s.networkWithdrawContract.GetEjectedValidatorsAtCycle(nil, big.NewInt(willDealCycle))
	if err != nil {
		return fmt.Errorf("GetEjectedValidatorsAtCycle failed: %w", err)
	}
	// return if already dealt
	if len(ejectedValidator) != 0 {
		s.log.Debugf("ejectedValidator %d at cycle %d", len(ejectedValidator), willDealCycle)
		return nil
	}

	userDepositBalance, err := s.userDepositContract.GetBalance(targetCall)
	if err != nil {
		return err
	}

	s.log.WithFields(logrus.Fields{
		"currentCycle:":      currentCycle,
		"targetEpoch":        targetEpoch,
		"targetBlockNumber":  targetBlockNumber,
		"totalMissingAmount": totalMissingAmount,
		"userDepositBalance": userDepositBalance,
	}).Debug("notifyValidatorExit")

	// calc exited but not full withdrawed amount
	exitButNotFullWithdrawedValidatorList, err := s.exitButNotFullWithdrawedValidatorListAtEpoch(ctx, targetEpoch)
	if err != nil {
		return errors.Wrap(err, "exitButNotFullWithdrawedValidatorListAtEpoch failed")
	}
	totalExitedButNotDistributedUserAmount := decimal.Zero
	for _, v := range exitButNotFullWithdrawedValidatorList {
		totalExitedButNotDistributedUserAmount = totalExitedButNotDistributedUserAmount.Add(utils.StandardEffectiveBalanceDeci.Sub(v.NodeDepositAmountDeci))
	}

	// calc withdrawals(partial/full) but not distributed amount
	latestDistributeWithdrawalHeight, err := s.networkWithdrawContract.LatestDistributeWithdrawalsHeight(targetCall)
	if err != nil {
		return err
	}
	userUndistributedWithdrawalsDeci, _, _, _, err := s.getUserNodePlatformFromWithdrawals(latestDistributeWithdrawalHeight.Uint64(), targetBlockNumber)
	if err != nil {
		return errors.Wrap(err, "getUserNodePlatformFromWithdrawals failed")
	}

	totalPendingAmountDeci := totalExitedButNotDistributedUserAmount.Add(userUndistributedWithdrawalsDeci)

	// no need notify exit
	if totalMissingAmountDeci.LessThanOrEqual(totalPendingAmountDeci) {
		return nil
	}

	// final total missing amount
	finalTotalMissingAmountDeci := totalMissingAmountDeci.Sub(totalPendingAmountDeci)

	selectVals, err := s.mustSelectValidatorsForExit(ctx, finalTotalMissingAmountDeci, targetEpoch, uint64(willDealCycle))
	if err != nil {
		return errors.Wrap(err, "selectValidatorsForExit failed")
	}
	if len(selectVals) == 0 {
		return fmt.Errorf("selectValidatorsForExit select zero vals, target epoch: %d", targetEpoch)
	}

	// cal start cycle
	startCycle := willDealCycle - 1
	notExitElectionList := s.notExitElectionListBefore(targetEpoch, uint64(willDealCycle))
	if err != nil {
		return errors.Wrap(err, "GetAllNotExitElectionList failed")
	}
	if len(notExitElectionList) > 0 {
		startCycle = int64(notExitElectionList[0].WithdrawCycle)
	}

	// ---- send NotifyValidatorExit tx
	return s.sendNotifyExitTx(uint64(willDealCycle), uint64(startCycle), selectVals)
}

func (s *Service) sendNotifyExitTx(withdrawCycle, startCycle uint64, selectVals []*big.Int) error {
	err := s.connection.LockAndUpdateTxOpts()
	if err != nil {
		return fmt.Errorf("LockAndUpdateTxOpts err: %w", err)
	}
	defer s.connection.UnlockTxOpts()

	encodeBts, err := s.networkWithdrawAbi.Pack("notifyValidatorExit", big.NewInt(int64(withdrawCycle)),
		big.NewInt(int64(startCycle)), selectVals)
	if err != nil {
		return err
	}

	proposalId := utils.ProposalId(s.networkWithdrawAddress, encodeBts, big.NewInt(int64(withdrawCycle)))

	// check voted
	hasVoted, err := s.networkProposalContract.HasVoted(nil, proposalId, s.connection.Keypair().CommonAddress())
	if err != nil {
		return fmt.Errorf("networkProposalContract.HasVoted err: %s", err)
	}
	if hasVoted {
		return nil
	}

	s.log.WithFields(logrus.Fields{
		"startCycle":      startCycle,
		"withdrawalCycle": withdrawCycle,
		"selectVal":       selectVals,
	}).Debug("will sendNotifyValidatorExitTx")

	opts := s.connection.TxOpts()
	opts.GasLimit = 15000000
	tx, err := s.networkProposalContract.ExecProposal(opts, s.networkWithdrawAddress, encodeBts, big.NewInt(int64(withdrawCycle)))
	if err != nil {
		return err
	}

	s.log.Info("send NotifyValidatorExit tx hash: ", tx.Hash().String())

	return s.waitProposalTxOk(tx.Hash(), proposalId)
}

func (s *Service) currentCycleAndStartTimestamp() (int64, int64, error) {
	currentCycle := uint64(time.Now().Unix()) / s.cycleSeconds

	targetTimestamp := uint64(currentCycle) * s.cycleSeconds
	return int64(currentCycle), int64(targetTimestamp), nil
}

func (s *Service) mustSelectValidatorsForExit(ctx context.Context, totalMissingAmount decimal.Decimal, targetEpoch, willDealCycle uint64) ([]*big.Int, error) {
	vals, err := s.getValidatorsOfTargetEpoch(ctx, targetEpoch)
	if err != nil {
		return nil, err
	}

	// sort by active epoch
	sort.SliceStable(vals, func(i, j int) bool {
		return vals[i].ActiveEpoch < vals[j].ActiveEpoch
	})

	type ElectedValidator struct {
		Index         uint64
		WithdrawCycle uint64
	}

	electedValidators := make(map[uint64]*ElectedValidator)
	for _, election := range s.exitElections {
		for _, valIndex := range election.ValidatorIndexList {

			electedValidators[valIndex] = &ElectedValidator{
				Index:         valIndex,
				WithdrawCycle: election.WithdrawCycle,
			}
		}
	}

	selectVal := make([]*big.Int, 0)
	totalExitAmountDeci := decimal.Zero
	for _, val := range vals {
		// skip if exist in election list
		if eval, exist := electedValidators[val.ValidatorIndex]; exist && eval.WithdrawCycle < willDealCycle {
			continue
		}

		userAmountDeci := utils.StandardEffectiveBalanceDeci.Sub(val.NodeDepositAmountDeci)
		totalExitAmountDeci = totalExitAmountDeci.Add(userAmountDeci)

		selectVal = append(selectVal, big.NewInt(int64(val.ValidatorIndex)))

		if totalExitAmountDeci.GreaterThanOrEqual(totalMissingAmount) {
			break
		}
	}

	sort.SliceStable(selectVal, func(i, j int) bool {
		return selectVal[i].Cmp(selectVal[j]) == -1
	})

	return selectVal, nil
}
