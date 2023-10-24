package service

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/connection/beacon"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/utils"
	"golang.org/x/sync/errgroup"
)

var ErrExceedsValidatorUpdateBlock = fmt.Errorf("ErrExceedsValidatorUpdateBlock")

// sync beacon and execution block info
func (s *Service) syncBlocks() error {
	beaconHead, err := s.connection.Eth2BeaconHead()
	if err != nil {
		return err
	}

	if beaconHead.FinalizedSlot <= s.latestSlotOfSyncBlock {
		return nil
	}
	latestSlotOfUpdateValidator := utils.EndSlotOfEpoch(s.eth2Config, s.latestEpochOfUpdateValidator)

	start := uint64(s.latestSlotOfSyncBlock + 1)
	end := beaconHead.FinalizedSlot
	if end > latestSlotOfUpdateValidator {
		end = latestSlotOfUpdateValidator
	}

	g := new(errgroup.Group)
	g.SetLimit(int(s.batchRequestBlocksNumber))

	for i := start; i <= end; i += s.batchRequestBlocksNumber {
		subStart := i
		subEnd := i + s.batchRequestBlocksNumber - 1
		if end < i+s.batchRequestBlocksNumber {
			subEnd = end
		}
		preLatestSyncBlock := s.latestBlockOfSyncBlock
		batchRequestStartTime := time.Now().Unix()

		blockReciever := make([]*beacon.BeaconBlock, s.batchRequestBlocksNumber)
		for j := subStart; j <= subEnd; j++ {
			// notice this
			slot := j
			g.Go(func() error {
				startTime := time.Now().Unix()
				beaconBlock, exist, err := s.connection.GetBeaconBlock(slot)
				if err != nil {
					return err
				}
				endTime := time.Now().Unix()

				// maybe not exist this slot
				if !exist {
					return nil
				}
				// wait validator updated
				if beaconBlock.ExecutionBlockNumber > s.latestBlockOfUpdateValidator {
					return ErrExceedsValidatorUpdateBlock
				}

				// ensure validator cached
				_, isPoolVal := s.getValidatorByIndex(beaconBlock.ProposerIndex)
				if isPoolVal {
					fee, err := s.connection.GetELRewardForBlock(beaconBlock.ExecutionBlockNumber)
					if err != nil {
						return err
					}
					beaconBlock.PriorityFee = fee
				}

				blockReciever[slot-subStart] = &beaconBlock

				saveTime := time.Now().Unix()
				logrus.Tracef("request block %d,start at %d, end at %d, save at: %d ", beaconBlock.ExecutionBlockNumber, startTime, endTime, saveTime)
				return nil
			})
		}

		err = g.Wait()
		if err != nil {
			s.latestBlockOfSyncBlock = preLatestSyncBlock
			if err == ErrExceedsValidatorUpdateBlock {
				return nil
			}

			logrus.Warnf("sync block err: %s, will retry", err.Error())
			return err
		}

		batchRequestWaitTime := time.Now().Unix()

		for _, beaconBlock := range blockReciever {
			if beaconBlock == nil {
				continue
			}
			logrus.Tracef("save block: %d", beaconBlock.ExecutionBlockNumber)

			if beaconBlock.ExecutionBlockNumber%500 == 0 {
				logrus.Infof("synced block: %d", beaconBlock.ExecutionBlockNumber)
			}

			cachedWithdrawals := make([]*CachedWithdrawal, len(beaconBlock.Withdrawals))
			for i, w := range beaconBlock.Withdrawals {
				cachedWithdrawals[i] = &CachedWithdrawal{
					ValidatorIndex: w.ValidatorIndex,
					Amount:         w.Amount,
				}
			}

			cachedTxs := make([]*CachedTransaction, len(beaconBlock.Transactions))
			for i, t := range beaconBlock.Transactions {
				cachedTxs[i] = &CachedTransaction{
					Recipient: t.Recipient,
					Amount:    t.Amount,
				}
			}

			s.cachedBeaconBlockMutex.Lock()
			s.cachedBeaconBlock[beaconBlock.ExecutionBlockNumber] = &CachedBeaconBlock{
				ProposerIndex: beaconBlock.ProposerIndex,
				Withdrawals:   cachedWithdrawals,
				FeeRecipient:  beaconBlock.FeeRecipient,
				Transactions:  cachedTxs,
				PriorityFee:   beaconBlock.PriorityFee,
			}
			s.cachedBeaconBlockMutex.Unlock()

			// update latest block
			if beaconBlock.ExecutionBlockNumber > s.latestBlockOfSyncBlock {
				s.latestBlockOfSyncBlock = beaconBlock.ExecutionBlockNumber
			}
		}

		// update latest slot
		s.latestSlotOfSyncBlock = subEnd

		batchRequestEndTime := time.Now().Unix()
		logrus.Tracef("batch request block, start at: %d, wait at %d, end at %d", batchRequestStartTime, batchRequestWaitTime, batchRequestEndTime)
	}

	return nil
}
