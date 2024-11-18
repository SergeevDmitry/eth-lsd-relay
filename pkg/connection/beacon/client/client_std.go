package client

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/connection/types"
	"golang.org/x/sync/errgroup"

	gtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/connection/beacon"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/utils"
	ethpb "github.com/stratisproject/prysm-stratis/proto/eth/v1"
)

// Config
const (
	RequestUrlFormat   = "%s%s"
	RequestContentType = "application/json"

	RequestSyncStatusPath            = "/eth/v1/node/syncing"
	RequestEth2ConfigPath            = "/eth/v1/config/spec"
	RequestEth2DepositContractMethod = "/eth/v1/config/deposit_contract"
	RequestGenesisPath               = "/eth/v1/beacon/genesis"
	RequestFinalityCheckpointsPath   = "/eth/v1/beacon/states/%s/finality_checkpoints"
	RequestValidatorsPath            = "/eth/v1/beacon/states/%s/validators"
	RequestVoluntaryExitPath         = "/eth/v1/beacon/pool/voluntary_exits"
	RequestBeaconBlockPath           = "/eth/v2/beacon/blocks/%d"

	MaxRequestValidatorsCount = 50
)

// Beacon client using the standard Beacon HTTP REST API (https://ethereum.github.io/beacon-APIs/)
type StandardHttpClient struct {
	providerAddress string
	eth2Config      beacon.Eth2Config
	signer          gtypes.Signer
}

// Create a new client instance
func NewStandardHttpClient(providerAddress string, chainID *big.Int) (*StandardHttpClient, error) {

	client := &StandardHttpClient{
		providerAddress: providerAddress,
	}
	config, err := client.GetEth2Config()
	if err != nil {
		return nil, err
	}
	client.eth2Config = config
	signer := gtypes.NewLondonSigner(chainID)
	client.signer = signer
	return client, nil
}

// Close the client connection
func (c *StandardHttpClient) Close() error {
	return nil
}

// Get the client's process configuration type
func (c *StandardHttpClient) GetClientType() (beacon.BeaconClientType, error) {
	return beacon.SplitProcess, nil
}

// Get the node's sync status
func (c *StandardHttpClient) GetSyncStatus() (beacon.SyncStatus, error) {

	// Get sync status
	syncStatus, err := c.getSyncStatus()
	if err != nil {
		return beacon.SyncStatus{}, err
	}

	// Calculate the progress
	progress := float64(syncStatus.Data.HeadSlot) / float64(syncStatus.Data.HeadSlot+syncStatus.Data.SyncDistance)

	// Return response
	return beacon.SyncStatus{
		Syncing:  syncStatus.Data.IsSyncing,
		Progress: progress,
	}, nil

}

// Get the eth2 config
func (c *StandardHttpClient) GetEth2Config() (beacon.Eth2Config, error) {

	// Data
	var wg errgroup.Group
	var eth2Config Eth2ConfigResponse
	var genesis GenesisResponse

	// Get eth2 config
	wg.Go(func() error {
		var err error
		eth2Config, err = c.getEth2Config()
		return err
	})

	// Get genesis
	wg.Go(func() error {
		var err error
		genesis, err = c.getGenesis()
		return err
	})

	// Wait for data
	if err := wg.Wait(); err != nil {
		return beacon.Eth2Config{}, err
	}

	// Return response
	return beacon.Eth2Config{
		GenesisForkVersion:           genesis.Data.GenesisForkVersion,
		GenesisValidatorsRoot:        genesis.Data.GenesisValidatorsRoot,
		GenesisEpoch:                 0,
		GenesisTime:                  uint64(genesis.Data.GenesisTime),
		SecondsPerSlot:               uint64(eth2Config.Data.SecondsPerSlot),
		SlotsPerEpoch:                uint64(eth2Config.Data.SlotsPerEpoch),
		SecondsPerEpoch:              uint64(eth2Config.Data.SecondsPerSlot * eth2Config.Data.SlotsPerEpoch),
		EpochsPerSyncCommitteePeriod: uint64(eth2Config.Data.EpochsPerSyncCommitteePeriod),
	}, nil

}

// Get the eth2 deposit contract info
func (c *StandardHttpClient) GetEth2DepositContract() (beacon.Eth2DepositContract, error) {

	// Get the deposit contract
	depositContract, err := c.getEth2DepositContract()
	if err != nil {
		return beacon.Eth2DepositContract{}, err
	}

	// Return response
	return beacon.Eth2DepositContract{
		ChainID: uint64(depositContract.Data.ChainID),
		Address: depositContract.Data.Address,
	}, nil
}

// Get the beacon head
func (c *StandardHttpClient) GetBeaconHead() (beacon.BeaconHead, error) {
	var finalityCheckpoints FinalityCheckpointsResponse
	finalityCheckpoints, err := c.getFinalityCheckpoints("head")

	if err != nil {
		return beacon.BeaconHead{}, err
	}

	epoch := utils.EpochAtTimestamp(c.eth2Config, uint64(time.Now().Unix()))
	// Return response
	return beacon.BeaconHead{
		Epoch:                  epoch,
		Slot:                   utils.StartSlotOfEpoch(c.eth2Config, epoch),
		FinalizedEpoch:         uint64(finalityCheckpoints.Data.Finalized.Epoch),
		FinalizedSlot:          utils.StartSlotOfEpoch(c.eth2Config, uint64(finalityCheckpoints.Data.Finalized.Epoch)),
		JustifiedEpoch:         uint64(finalityCheckpoints.Data.CurrentJustified.Epoch),
		PreviousJustifiedEpoch: uint64(finalityCheckpoints.Data.PreviousJustified.Epoch),
	}, nil
}

// Get a validator's status
func (c *StandardHttpClient) GetValidatorStatus(ctx context.Context, pubkey types.ValidatorPubkey, opts *beacon.ValidatorStatusOptions) (beacon.ValidatorStatus, error) {
	return c.getValidatorStatus(ctx, utils.AddPrefix(pubkey.Hex()), opts)
}

func (c *StandardHttpClient) getValidatorStatus(ctx context.Context, pubkeyOrIndex string, opts *beacon.ValidatorStatusOptions) (beacon.ValidatorStatus, error) {
	// Return zero status for null pubkeyOrIndex
	if pubkeyOrIndex == "" {
		return beacon.ValidatorStatus{}, nil
	}

	// Get validator
	validators, err := c.getValidatorsByOpts(ctx, []string{pubkeyOrIndex}, opts)
	if err != nil {
		return beacon.ValidatorStatus{}, err
	}
	if len(validators.Data) == 0 {
		return beacon.ValidatorStatus{}, nil
	}
	validator := validators.Data[0]

	// Return response
	return beacon.ValidatorStatus{
		Pubkey:                     types.BytesToValidatorPubkey(validator.Validator.Pubkey),
		Index:                      uint64(validator.Index),
		WithdrawalCredentials:      common.BytesToHash(validator.Validator.WithdrawalCredentials),
		Balance:                    uint64(validator.Balance),
		EffectiveBalance:           uint64(validator.Validator.EffectiveBalance),
		Slashed:                    validator.Validator.Slashed,
		ActivationEligibilityEpoch: uint64(validator.Validator.ActivationEligibilityEpoch),
		ActivationEpoch:            uint64(validator.Validator.ActivationEpoch),
		ExitEpoch:                  uint64(validator.Validator.ExitEpoch),
		WithdrawableEpoch:          uint64(validator.Validator.WithdrawableEpoch),
		Exists:                     true,
		Status:                     ethpb.ValidatorStatus(ethpb.ValidatorStatus_value[strings.ToUpper(validator.Status)]),
	}, nil

}

// Get multiple validators' statuses
// epoch in opts == the first slot of epoch
func (c *StandardHttpClient) GetValidatorStatuses(ctx context.Context, pubkeys []types.ValidatorPubkey, opts *beacon.ValidatorStatusOptions) (map[types.ValidatorPubkey]beacon.ValidatorStatus, error) {

	// The null validator pubkey
	nullPubkey := types.ValidatorPubkey{}

	// Filter out null pubkeys
	nullPubkeyExists := false
	for _, pubkey := range pubkeys {
		if bytes.Equal(pubkey.Bytes(), nullPubkey.Bytes()) {
			nullPubkeyExists = true
		}
	}

	// Convert pubkeys into hex strings
	pubkeysHex := make([]string, len(pubkeys))
	for vi := 0; vi < len(pubkeys); vi++ {
		pubkeysHex[vi] = utils.AddPrefix(pubkeys[vi].Hex())
	}

	// Get validators
	validators, err := c.getValidatorsByOpts(ctx, pubkeysHex, opts)
	if err != nil {
		return nil, err
	}

	// Build validator status map
	statuses := make(map[types.ValidatorPubkey]beacon.ValidatorStatus)
	for _, validator := range validators.Data {

		// Get validator pubkey
		pubkey := types.BytesToValidatorPubkey(validator.Validator.Pubkey)

		// Add status
		statuses[pubkey] = beacon.ValidatorStatus{
			Pubkey:                     types.BytesToValidatorPubkey(validator.Validator.Pubkey),
			Index:                      uint64(validator.Index),
			WithdrawalCredentials:      common.BytesToHash(validator.Validator.WithdrawalCredentials),
			Balance:                    uint64(validator.Balance),
			EffectiveBalance:           uint64(validator.Validator.EffectiveBalance),
			Slashed:                    validator.Validator.Slashed,
			ActivationEligibilityEpoch: uint64(validator.Validator.ActivationEligibilityEpoch),
			ActivationEpoch:            uint64(validator.Validator.ActivationEpoch),
			ExitEpoch:                  uint64(validator.Validator.ExitEpoch),
			WithdrawableEpoch:          uint64(validator.Validator.WithdrawableEpoch),
			Exists:                     true,
			Status:                     ethpb.ValidatorStatus(ethpb.ValidatorStatus_value[strings.ToUpper(validator.Status)]),
		}

	}

	// Add zero status for null pubkey if requested
	if nullPubkeyExists {
		statuses[nullPubkey] = beacon.ValidatorStatus{}
	}

	// Return
	return statuses, nil

}

// Perform a voluntary exit on a validator
func (c *StandardHttpClient) ExitValidator(validatorIndex, epoch uint64, signature types.ValidatorSignature) error {
	return c.postVoluntaryExit(VoluntaryExitRequest{
		Message: VoluntaryExitMessage{
			Epoch:          uinteger(epoch),
			ValidatorIndex: uinteger(validatorIndex),
		},
		Signature: signature.Bytes(),
	})
}

// Get the ETH1 data for the target beacon block
func (c *StandardHttpClient) GetEth1DataForEth2Block(blockId uint64) (beacon.Eth1Data, bool, error) {

	// Get the Beacon block
	block, exists, err := c.getBeaconBlock(blockId)
	if err != nil {
		return beacon.Eth1Data{}, false, err
	}
	if !exists {
		return beacon.Eth1Data{}, false, nil
	}

	// Convert the response to the eth1 data struct
	return beacon.Eth1Data{
		DepositRoot:  common.HexToHash(block.Data.Message.Body.Eth1Data.DepositRoot),
		DepositCount: uint64(block.Data.Message.Body.Eth1Data.DepositCount),
		BlockHash:    common.HexToHash(block.Data.Message.Body.Eth1Data.BlockHash),
	}, true, nil

}

func (c *StandardHttpClient) GetBeaconBlock(blockId uint64) (beacon.BeaconBlock, bool, error) {
	block, exists, err := c.getBeaconBlock(blockId)
	if err != nil {
		return beacon.BeaconBlock{}, false, err
	}
	if !exists {
		return beacon.BeaconBlock{}, false, nil
	}

	beaconBlock := beacon.BeaconBlock{
		Slot:          uint64(block.Data.Message.Slot),
		ProposerIndex: uint64(block.Data.Message.ProposerIndex),
	}

	// Add attestation info
	for i, attestation := range block.Data.Message.Body.Attestations {
		bitString := utils.RemovePrefix(attestation.AggregationBits)
		info := beacon.AttestationInfo{
			SlotIndex:      uint64(attestation.Data.Slot),
			CommitteeIndex: uint64(attestation.Data.Index),
		}
		info.AggregationBits, err = hex.DecodeString(bitString)
		if err != nil {
			return beacon.BeaconBlock{}, false, fmt.Errorf("decoding aggregation bits for attestation %d of block %d err: %w", i, blockId, err)
		}
		beaconBlock.Attestations = append(beaconBlock.Attestations, info)
	}

	//add syncAggregate
	if len(block.Data.Message.Body.SyncAggregate.SyncCommitteeBits) > 0 {
		syncAggregate := beacon.SyncAggregate{}
		bitString := utils.RemovePrefix(block.Data.Message.Body.SyncAggregate.SyncCommitteeBits)
		syncAggregate.SyncCommitteeBits, err = hex.DecodeString(bitString)
		if err != nil {
			return beacon.BeaconBlock{}, false, fmt.Errorf("decoding aggregation bits for SyncCommitteeBits of block %d err: %w", blockId, err)
		}
		syncAggregate.SyncCommitteeSignature = block.Data.Message.Body.SyncAggregate.SyncCommitteeSignature

		beaconBlock.SyncAggregate = syncAggregate
	}

	// Add proposer slash
	for _, proposerSlash := range block.Data.Message.Body.ProposerSlashings {
		newProposerSlash := beacon.ProposerSlashing{
			SignedHeader1: beacon.SignedHeader{
				Slot:          uint64(proposerSlash.SignedHeader1.Message.Slot),
				ProposerIndex: uint64(proposerSlash.SignedHeader1.Message.ProposerIndex),
				ParentRoot:    proposerSlash.SignedHeader1.Message.ParentRoot,
				StateRoot:     proposerSlash.SignedHeader1.Message.StateRoot,
				BodyRoot:      proposerSlash.SignedHeader1.Message.BodyRoot,
				Signature:     proposerSlash.SignedHeader1.Signature,
			},
			SignedHeader2: beacon.SignedHeader{
				Slot:          uint64(proposerSlash.SignedHeader2.Message.Slot),
				ProposerIndex: uint64(proposerSlash.SignedHeader2.Message.ProposerIndex),
				ParentRoot:    proposerSlash.SignedHeader2.Message.ParentRoot,
				StateRoot:     proposerSlash.SignedHeader2.Message.StateRoot,
				BodyRoot:      proposerSlash.SignedHeader2.Message.BodyRoot,
				Signature:     proposerSlash.SignedHeader2.Signature,
			},
		}
		beaconBlock.ProposerSlashings = append(beaconBlock.ProposerSlashings, newProposerSlash)
	}

	// Add attester slash
	for _, attesterSlash := range block.Data.Message.Body.AttesterSlashings {
		newAttestingIndeces1 := make([]uint64, len(attesterSlash.Attestation1.AttestingIndices))
		for i, indice := range attesterSlash.Attestation1.AttestingIndices {
			newAttestingIndeces1[i] = uint64(indice)
		}
		newAttestingIndeces2 := make([]uint64, len(attesterSlash.Attestation2.AttestingIndices))
		for i, indice := range attesterSlash.Attestation2.AttestingIndices {
			newAttestingIndeces2[i] = uint64(indice)
		}
		newAttesterSalsh := beacon.AttesterSlashing{
			Attestation1: beacon.Attestation{
				AttestingIndices: newAttestingIndeces1,
				Signature:        attesterSlash.Attestation1.Signature,
				Slot:             uint64(attesterSlash.Attestation1.Data.Slot),
				Index:            uint64(attesterSlash.Attestation1.Data.Index),
				BeaconBlockRoot:  attesterSlash.Attestation1.Data.BeaconBlockRoot,
				SourceEpoch:      uint64(attesterSlash.Attestation1.Data.Source.Epoch),
				SourceRoot:       attesterSlash.Attestation1.Data.Source.Root,
				TargetEpoch:      uint64(attesterSlash.Attestation1.Data.Target.Epoch),
				TargetRoot:       attesterSlash.Attestation1.Data.Target.Root,
			},
			Attestation2: beacon.Attestation{
				AttestingIndices: newAttestingIndeces2,
				Signature:        attesterSlash.Attestation2.Signature,
				Slot:             uint64(attesterSlash.Attestation2.Data.Slot),
				Index:            uint64(attesterSlash.Attestation2.Data.Index),
				BeaconBlockRoot:  attesterSlash.Attestation2.Data.BeaconBlockRoot,
				SourceEpoch:      uint64(attesterSlash.Attestation2.Data.Source.Epoch),
				SourceRoot:       attesterSlash.Attestation2.Data.Source.Root,
				TargetEpoch:      uint64(attesterSlash.Attestation2.Data.Target.Epoch),
				TargetRoot:       attesterSlash.Attestation2.Data.Target.Root,
			},
		}
		beaconBlock.AttesterSlashing = append(beaconBlock.AttesterSlashing, newAttesterSalsh)
	}

	for _, withdrawal := range block.Data.Message.Body.ExecutionPayload.Withdrawals {
		beaconBlock.Withdrawals = append(beaconBlock.Withdrawals, beacon.Withdrawal{
			WithdrawIndex:  uint64(withdrawal.Index),
			ValidatorIndex: uint64(withdrawal.ValidatorIndex),
			Address:        common.HexToAddress(withdrawal.Address),
			Amount:         uint64(withdrawal.Amount),
		})
	}

	for _, exitMsg := range block.Data.Message.Body.VoluntaryExits {
		beaconBlock.VoluntaryExits = append(beaconBlock.VoluntaryExits, beacon.VoluntaryExit{
			ValidatorIndex: uint64(exitMsg.Message.ValidatorIndex),
			Epoch:          uint64(exitMsg.Message.Epoch),
		})
	}

	// Execution payload only exists after the merge, so check for its existence
	if block.Data.Message.Body.ExecutionPayload != nil {
		beaconBlock.ExecutionBlockNumber = uint64(block.Data.Message.Body.ExecutionPayload.BlockNumber)
	}

	return beaconBlock, true, nil
}

// Get sync status
func (c *StandardHttpClient) getSyncStatus() (SyncStatusResponse, error) {
	responseBody, status, err := c.getRequest(RequestSyncStatusPath)
	if err != nil {
		return SyncStatusResponse{}, fmt.Errorf("could not get node sync status: %w", err)
	}
	if status != http.StatusOK {
		return SyncStatusResponse{}, fmt.Errorf("could not get node sync status: HTTP status %d; response body: '%s'", status, string(responseBody))
	}
	var syncStatus SyncStatusResponse
	if err := json.Unmarshal(responseBody, &syncStatus); err != nil {
		return SyncStatusResponse{}, fmt.Errorf("could not decode node sync status: %w", err)
	}
	return syncStatus, nil
}

// Get the eth2 config
func (c *StandardHttpClient) getEth2Config() (Eth2ConfigResponse, error) {
	responseBody, status, err := c.getRequest(RequestEth2ConfigPath)
	if err != nil {
		return Eth2ConfigResponse{}, fmt.Errorf("could not get eth2 config: %w", err)
	}
	if status != http.StatusOK {
		return Eth2ConfigResponse{}, fmt.Errorf("could not get eth2 config: HTTP status %d; response body: '%s'", status, string(responseBody))
	}
	var eth2Config Eth2ConfigResponse
	if err := json.Unmarshal(responseBody, &eth2Config); err != nil {
		return Eth2ConfigResponse{}, fmt.Errorf("could not decode eth2 config: %w", err)
	}
	return eth2Config, nil
}

// Get the eth2 deposit contract info
func (c *StandardHttpClient) getEth2DepositContract() (Eth2DepositContractResponse, error) {
	responseBody, status, err := c.getRequest(RequestEth2DepositContractMethod)
	if err != nil {
		return Eth2DepositContractResponse{}, fmt.Errorf("could not get eth2 deposit contract: %w", err)
	}
	if status != http.StatusOK {
		return Eth2DepositContractResponse{}, fmt.Errorf("could not get eth2 deposit contract: HTTP status %d; response body: '%s'", status, string(responseBody))
	}
	var eth2DepositContract Eth2DepositContractResponse
	if err := json.Unmarshal(responseBody, &eth2DepositContract); err != nil {
		return Eth2DepositContractResponse{}, fmt.Errorf("could not decode eth2 deposit contract: %w", err)
	}
	return eth2DepositContract, nil
}

// Get genesis information
func (c *StandardHttpClient) getGenesis() (GenesisResponse, error) {
	responseBody, status, err := c.getRequest(RequestGenesisPath)
	if err != nil {
		return GenesisResponse{}, fmt.Errorf("could not get genesis data: %w", err)
	}
	if status != http.StatusOK {
		return GenesisResponse{}, fmt.Errorf("could not get genesis data: HTTP status %d; response body: '%s'", status, string(responseBody))
	}
	var genesis GenesisResponse
	if err := json.Unmarshal(responseBody, &genesis); err != nil {
		return GenesisResponse{}, fmt.Errorf("could not decode genesis: %w", err)
	}
	return genesis, nil
}

// Get finality checkpoints
func (c *StandardHttpClient) getFinalityCheckpoints(stateId string) (FinalityCheckpointsResponse, error) {
	responseBody, status, err := c.getRequest(fmt.Sprintf(RequestFinalityCheckpointsPath, stateId))
	if err != nil {
		return FinalityCheckpointsResponse{}, fmt.Errorf("could not get finality checkpoints: %w", err)
	}
	if status != http.StatusOK {
		return FinalityCheckpointsResponse{}, fmt.Errorf("could not get finality checkpoints: HTTP status %d; response body: '%s'", status, string(responseBody))
	}
	var finalityCheckpoints FinalityCheckpointsResponse
	if err := json.Unmarshal(responseBody, &finalityCheckpoints); err != nil {
		return FinalityCheckpointsResponse{}, fmt.Errorf("could not decode finality checkpoints: %w", err)
	}
	return finalityCheckpoints, nil
}

// Get validators
func (c *StandardHttpClient) getValidators(ctx context.Context, stateId string, pubkeys []string) (ValidatorsResponse, error) {
	var query string
	if len(pubkeys) > 0 {
		query = fmt.Sprintf("?id=%s", strings.Join(pubkeys, ","))
	}
	responseBody, status, err := c.getRequest(fmt.Sprintf(RequestValidatorsPath, stateId)+query, ctx)
	if err != nil {
		return ValidatorsResponse{}, fmt.Errorf("could not get validators: %w", err)
	}
	if status != http.StatusOK {
		return ValidatorsResponse{}, fmt.Errorf("could not get validators: HTTP status %d; response body: '%s'", status, string(responseBody))
	}

	var validators ValidatorsResponse
	if err := json.Unmarshal(responseBody, &validators); err != nil {
		return ValidatorsResponse{}, fmt.Errorf("could not decode validators: %w", err)
	}
	return validators, nil
}

// Get validators by pubkeys and status options
func (c *StandardHttpClient) getValidatorsByOpts(ctx context.Context, pubkeysOrIndices []string, opts *beacon.ValidatorStatusOptions) (ValidatorsResponse, error) {

	// Get state ID
	var stateId string
	if opts == nil {
		stateId = "head"
	} else if opts.Slot != nil {
		stateId = strconv.FormatInt(int64(*opts.Slot), 10)
	} else if opts.Epoch != nil {
		// Get slot nuumber
		slot := *opts.Epoch * uint64(c.eth2Config.SlotsPerEpoch)
		stateId = strconv.FormatInt(int64(slot), 10)

	} else {
		return ValidatorsResponse{}, fmt.Errorf("must specify a slot or epoch when calling getValidatorsByOpts")
	}

	// Load validator data in batches & return
	data := make([]Validator, 0, len(pubkeysOrIndices))
	for bsi := 0; bsi < len(pubkeysOrIndices); bsi += MaxRequestValidatorsCount {

		// Get batch start & end index
		vsi := bsi
		vei := bsi + MaxRequestValidatorsCount
		if vei > len(pubkeysOrIndices) {
			vei = len(pubkeysOrIndices)
		}

		// Get validator pubkeysOrIndices for batch request
		batch := make([]string, vei-vsi)
		for vi := vsi; vi < vei; vi++ {
			batch[vi-vsi] = pubkeysOrIndices[vi]
		}
		validators, err := c.getValidators(ctx, stateId, batch)
		if err != nil {
			return ValidatorsResponse{}, err
		}

		data = append(data, validators.Data...)

	}
	return ValidatorsResponse{Data: data}, nil

}

// Send voluntary exit request
func (c *StandardHttpClient) postVoluntaryExit(request VoluntaryExitRequest) error {
	responseBody, status, err := c.postRequest(RequestVoluntaryExitPath, request)
	if err != nil {
		return fmt.Errorf("could not broadcast exit for validator at index %d: %w", request.Message.ValidatorIndex, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("could not broadcast exit for validator at index %d: HTTP status %d; response body: '%s'", request.Message.ValidatorIndex, status, string(responseBody))
	}
	return nil
}

// Get the target beacon block
func (c *StandardHttpClient) getBeaconBlock(blockId uint64) (BeaconBlockResponse, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	responseBody, status, err := c.getRequest(fmt.Sprintf(RequestBeaconBlockPath, blockId), ctx)
	if err != nil {
		return BeaconBlockResponse{}, false, fmt.Errorf("could not get beacon block data: %w", err)
	}
	if status == http.StatusNotFound {
		return BeaconBlockResponse{}, false, nil
	}
	if status != http.StatusOK {
		return BeaconBlockResponse{}, false, fmt.Errorf("could not get beacon block data: HTTP status %d; response body: '%s'", status, string(responseBody))
	}
	var beaconBlock BeaconBlockResponse
	if err := json.Unmarshal(responseBody, &beaconBlock); err != nil {
		return BeaconBlockResponse{}, false, fmt.Errorf("could not decode beacon block data: %w", err)
	}
	return beaconBlock, true, nil
}

// Make a GET request to the beacon node
func (c *StandardHttpClient) getRequest(requestPath string, optionalCtx ...context.Context) ([]byte, int, error) {
	var ctx context.Context
	if len(optionalCtx) == 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
	} else if len(optionalCtx) == 1 {
		ctx = optionalCtx[0]
	} else {
		return nil, 0, fmt.Errorf("you can pass only one context")
	}

	url := fmt.Sprintf(RequestUrlFormat, c.providerAddress, requestPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return []byte{}, 0, err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return []byte{}, 0, err
	}

	return body, response.StatusCode, nil
}

// Make a POST request to the beacon node
func (c *StandardHttpClient) postRequest(requestPath string, requestBody interface{}) ([]byte, int, error) {

	// Get request body
	requestBodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return []byte{}, 0, err
	}
	requestBodyReader := bytes.NewReader(requestBodyBytes)

	// Send request
	client := http.Client{Timeout: 60 * time.Second}
	response, err := client.Post(fmt.Sprintf(RequestUrlFormat, c.providerAddress, requestPath), RequestContentType, requestBodyReader)
	if err != nil {
		return []byte{}, 0, err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	// Get response
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return []byte{}, 0, err
	}

	// Return
	return body, response.StatusCode, nil

}
