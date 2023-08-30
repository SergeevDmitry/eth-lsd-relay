package service

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/prysmaticlabs/prysm/v3/beacon-chain/core/signing"
	"github.com/prysmaticlabs/prysm/v3/config/params"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
	"github.com/stafiprotocol/chainbridge/utils/crypto/secp256k1"
	deposit_contract "github.com/stafiprotocol/eth-lsd-relay/bindings/DepositContract"
	"github.com/stafiprotocol/eth-lsd-relay/bindings/LsdNetworkFactory"
	"github.com/stafiprotocol/eth-lsd-relay/bindings/NetworkWithdraw"
	"github.com/stafiprotocol/eth-lsd-relay/bindings/NodeDeposit"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/config"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/connection"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/connection/beacon"
	"github.com/stafiprotocol/eth-lsd-relay/pkg/utils"
)

var (
	lsdNetworkFactoryAddressMainnet = common.HexToAddress("")
	lsdNetworkFactoryAddressTestnet = common.HexToAddress("")
)

type Service struct {
	stop         chan struct{}
	eth1Endpoint string
	eth2Endpoint string
	keyPair      *secp256k1.Keypair
	gasLimit     *big.Int
	maxGasPrice  *big.Int

	// --- need init on start
	dev             bool
	eth1StartHeight uint64

	connection          *connection.Connection
	eth1Client          *ethclient.Client
	eth2Config          beacon.Eth2Config
	withdrawCredentials []byte
	domain              []byte // for eth2 sigs

	lsdNetworkFactoryAdress common.Address
	lsdTokenAdress          common.Address

	lsdNetworkFactoryContract *lsd_network_factory.LsdNetworkFactory
	nodeDepositContract       *node_deposit.NodeDeposit
	networkWithdrawContract   *network_withdraw.NetworkWithdraw
	depositContract           *deposit_contract.DepositContract

	quenedHandlers []Handler

	dealedEth1Block uint64

	govDeposits      map[string][][]byte // pubkey -> withdrawalCredentials
	govDepositsMutex sync.RWMutex

	validators      map[string]*Validator // pubkey -> validator
	validatorsMutex sync.RWMutex

	nodes      map[common.Address]*Node // nodeAddress -> node
	nodesMutex sync.RWMutex
}

type Node struct {
	NodeAddress common.Address
	NodeType    uint8 // 1 light node 2 trust node
}
type Validator struct {
	Pubkey []byte

	NodeAddress       common.Address
	DepositSignature  []byte
	NodeDepositAmount *big.Int
	DepositBlock      uint64
	ActiveEpoch       uint64
	EligibleEpoch     uint64
	ExitEpoch         uint64
	WithdrawableEpoch uint64
	NodeType          uint8  // 1 light node 2 trust node
	ValidatorIndex    uint64 // Notice!!!!!!: validator index is zero before status waiting

	Balance          uint64 `gorm:"type:bigint(20) unsigned not null;default:0;column:balance"`           // realtime balance
	EffectiveBalance uint64 `gorm:"type:bigint(20) unsigned not null;default:0;column:effective_balance"` // realtime effectiveBalance
	Status           uint8  // status details defined in pkg/utils/eth2.go
}

type Handler struct {
	method func() error
	name   string
}

func NewService(cfg *config.Config, keyPair *secp256k1.Keypair) (*Service, error) {
	if !common.IsHexAddress(cfg.Contracts.LsdTokenAddress) {
		return nil, fmt.Errorf("SsvTokenAddress contract address fmt err")
	}

	gasLimitDeci, err := decimal.NewFromString(cfg.GasLimit)
	if err != nil {
		return nil, err
	}

	if gasLimitDeci.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("gas limit is zero")
	}
	maxGasPriceDeci, err := decimal.NewFromString(cfg.MaxGasPrice)
	if err != nil {
		return nil, err
	}
	if maxGasPriceDeci.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("max gas price is zero")
	}

	eth1client, err := ethclient.Dial(cfg.Eth1Endpoint)
	if err != nil {
		return nil, err
	}
	s := &Service{
		stop:           make(chan struct{}),
		eth1Endpoint:   cfg.Eth1Endpoint,
		eth2Endpoint:   cfg.Eth2Endpoint,
		eth1Client:     eth1client,
		lsdTokenAdress: common.HexToAddress(cfg.Contracts.LsdTokenAddress),
		keyPair:        keyPair,
		gasLimit:       gasLimitDeci.BigInt(),
		maxGasPrice:    maxGasPriceDeci.BigInt(),
	}

	return s, nil
}

func (s *Service) Start() error {
	var err error
	s.connection, err = connection.NewConnection(s.eth1Endpoint, s.eth2Endpoint, s.keyPair,
		s.gasLimit, s.maxGasPrice)
	if err != nil {
		return err
	}

	chainId, err := s.eth1Client.ChainID(context.Background())
	if err != nil {
		return err
	}

	s.eth2Config, err = s.connection.Eth2Client().GetEth2Config()
	if err != nil {
		return err
	}

	switch chainId.Uint64() {
	case 1: //mainnet
		s.dev = false
		if !bytes.Equal(s.eth2Config.GenesisForkVersion, params.MainnetConfig().GenesisForkVersion) {
			return fmt.Errorf("endpoint network not match")
		}
		s.dealedEth1Block = 17705353
		s.lsdNetworkFactoryAdress = lsdNetworkFactoryAddressMainnet

		domain, err := signing.ComputeDomain(
			params.MainnetConfig().DomainDeposit,
			params.MainnetConfig().GenesisForkVersion,
			params.MainnetConfig().ZeroHash[:],
		)
		if err != nil {
			return err
		}
		s.domain = domain

	case 11155111: // sepolia
		s.dev = true
		if !bytes.Equal(s.eth2Config.GenesisForkVersion, params.SepoliaConfig().GenesisForkVersion) {
			return fmt.Errorf("endpoint network not match")
		}
		s.dealedEth1Block = 9354882
		s.lsdNetworkFactoryAdress = lsdNetworkFactoryAddressTestnet

		domain, err := signing.ComputeDomain(
			params.SepoliaConfig().DomainDeposit,
			params.SepoliaConfig().GenesisForkVersion,
			params.SepoliaConfig().ZeroHash[:],
		)
		if err != nil {
			return err
		}
		s.domain = domain
	case 5: // goerli
		s.dev = true
		if !bytes.Equal(s.eth2Config.GenesisForkVersion, params.PraterConfig().GenesisForkVersion) {
			return fmt.Errorf("endpoint network not match")
		}
		s.dealedEth1Block = 9403883
		s.lsdNetworkFactoryAdress = lsdNetworkFactoryAddressTestnet
		domain, err := signing.ComputeDomain(
			params.PraterConfig().DomainDeposit,
			params.PraterConfig().GenesisForkVersion,
			params.PraterConfig().ZeroHash[:],
		)
		if err != nil {
			return err
		}
		s.domain = domain
	default:
		return fmt.Errorf("unsupport chainId: %d", chainId.Int64())
	}
	if err != nil {
		return err
	}

	// init dealed eth1 block
	latestBlockNumber, err := s.connection.Eth1LatestBlock()
	if err != nil {
		return err
	}
	if latestBlockNumber > depositEventPreBlocks {
		s.dealedEth1Block = latestBlockNumber - depositEventPreBlocks
	}

	logrus.Info("init contracts...")
	err = s.initContract()
	if err != nil {
		return err
	}

	logrus.Info("start services...")
	s.appendHandlers(s.syncDepositInfo, s.updateValidatorsFromNetwork, s.updateValidatorsFromBeacon,
		s.voteWithdrawCredentials)

	utils.SafeGo(s.voteService)

	return nil
}

func (s *Service) Stop() {
	close(s.stop)
}

func (s *Service) initContract() error {
	var err error
	s.lsdNetworkFactoryContract, err = lsd_network_factory.NewLsdNetworkFactory(s.lsdNetworkFactoryAdress, s.eth1Client)
	if err != nil {
		return err
	}

	networkContracts, err := s.lsdNetworkFactoryContract.NetworkContractsOf(nil, s.lsdTokenAdress)
	if err != nil {
		return err
	}

	s.eth1StartHeight = networkContracts.Block.Uint64()

	s.nodeDepositContract, err = node_deposit.NewNodeDeposit(networkContracts.NodeDeposit, s.eth1Client)
	if err != nil {
		return err
	}
	s.networkWithdrawContract, err = network_withdraw.NewNetworkWithdraw(networkContracts.NodeDeposit, s.eth1Client)
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) voteService() {
	logrus.Info("start ssv service")
	retry := 0

Out:
	for {
		if retry > utils.RetryLimit {
			utils.ShutdownRequestChannel <- struct{}{}
			return
		}

		select {
		case <-s.stop:
			logrus.Info("task has stopped")
			return
		default:

			for _, handler := range s.quenedHandlers {
				funcName := handler.name
				logrus.Debugf("handler %s start.........", funcName)

				err := handler.method()
				if err != nil {
					logrus.Warnf("handler %s failed: %s, will retry.", funcName, err)
					time.Sleep(utils.RetryInterval * 4)
					retry++
					continue Out
				}
				logrus.Debugf("handler %s end.........", funcName)
			}

			retry = 0
		}

		time.Sleep(48 * time.Second) // 48 blocks
	}
}

func (s *Service) appendHandlers(handlers ...func() error) {
	for _, handler := range handlers {

		funcNameRaw := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()

		splits := strings.Split(funcNameRaw, "/")
		funcName := splits[len(splits)-1]

		s.quenedHandlers = append(s.quenedHandlers, Handler{
			method: handler,
			name:   funcName,
		})
	}
}

func (s *Service) waitTxOk(txHash common.Hash) error {
	_, err := utils.WaitTxOkCommon(s.eth1Client, txHash)
	if err != nil {
		return err
	}
	return nil
}