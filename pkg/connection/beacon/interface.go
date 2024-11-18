package beacon

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/prysmaticlabs/go-bitfield"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/connection/types"
	ethpb "github.com/stratisproject/prysm-stratis/proto/eth/v1"
)

// API request options
type ValidatorStatusOptions struct {
	Epoch *uint64
	Slot  *uint64
}

// API response types
type SyncStatus struct {
	Syncing  bool
	Progress float64
}
type Eth2Config struct {
	GenesisForkVersion           []byte
	GenesisValidatorsRoot        []byte
	GenesisEpoch                 uint64
	GenesisTime                  uint64
	SecondsPerSlot               uint64
	SlotsPerEpoch                uint64
	SecondsPerEpoch              uint64
	EpochsPerSyncCommitteePeriod uint64
}
type Eth2DepositContract struct {
	ChainID uint64
	Address common.Address
}
type BeaconHead struct {
	Epoch                  uint64
	Slot                   uint64
	FinalizedEpoch         uint64
	FinalizedSlot          uint64
	JustifiedEpoch         uint64
	PreviousJustifiedEpoch uint64
}
type ValidatorStatus struct {
	Pubkey                     types.ValidatorPubkey
	Index                      uint64
	WithdrawalCredentials      common.Hash
	Balance                    uint64
	EffectiveBalance           uint64
	Slashed                    bool
	ActivationEligibilityEpoch uint64
	ActivationEpoch            uint64
	ExitEpoch                  uint64
	WithdrawableEpoch          uint64
	Exists                     bool
	Status                     ethpb.ValidatorStatus
}
type Eth1Data struct {
	DepositRoot  common.Hash
	DepositCount uint64
	BlockHash    common.Hash
}
type BeaconBlock struct {
	Slot uint64

	// consensus
	ProposerIndex     uint64
	Attestations      []AttestationInfo
	ProposerSlashings []ProposerSlashing
	AttesterSlashing  []AttesterSlashing
	Withdrawals       []Withdrawal
	VoluntaryExits    []VoluntaryExit
	SyncAggregate     SyncAggregate

	// execute layer
	ExecutionBlockNumber uint64
}

type Withdrawal struct {
	WithdrawIndex  uint64
	ValidatorIndex uint64
	Address        common.Address
	Amount         uint64
}

type VoluntaryExit struct {
	ValidatorIndex uint64
	Epoch          uint64
}

type SyncAggregate struct {
	SyncCommitteeBits      bitfield.Bitlist
	SyncCommitteeSignature string
}

type ProposerSlashing struct {
	SignedHeader1 SignedHeader
	SignedHeader2 SignedHeader
}

type SignedHeader struct {
	Slot          uint64
	ProposerIndex uint64
	ParentRoot    string
	StateRoot     string
	BodyRoot      string
	Signature     string
}

type AttesterSlashing struct {
	Attestation1 Attestation
	Attestation2 Attestation
}

type Attestation struct {
	AttestingIndices []uint64
	Signature        string
	Slot             uint64
	Index            uint64
	BeaconBlockRoot  string
	SourceEpoch      uint64
	SourceRoot       string
	TargetEpoch      uint64
	TargetRoot       string
}

type Committee struct {
	Index      uint64
	Slot       uint64
	Validators []uint64
}

type SyncCommittee struct {
	ValIndex uint64
}

type AttestationInfo struct {
	AggregationBits bitfield.Bitlist
	SlotIndex       uint64
	CommitteeIndex  uint64
}

// Beacon client type
type BeaconClientType int

const (
	// This client is a traditional "split process" design, where the beacon
	// client and validator process are separate and run in different
	// containers
	SplitProcess BeaconClientType = iota

	// This client is a "single process" where the beacon client and
	// validator run in the same process (or run as separate processes
	// within the same docker container)
	SingleProcess

	// Unknown / missing client type
	Unknown
)
