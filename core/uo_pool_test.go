package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/contracts/aatestcontracts"
	"github.com/ethereum/go-ethereum/contracts/entrypoint"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
)

type testBlockChainWithContext struct {
	testBlockChain
}

func (bc *testBlockChainWithContext) Engine() consensus.Engine {
	return ethash.NewFullFaker()
}

func (bc *testBlockChainWithContext) GetHeader(hash common.Hash, number uint64) *types.Header {
	return bc.GetBlock(hash, number).Header()
}

type testMevPool []common.Hash

func (mevPool *testMevPool) AddMevBundle(txs types.Transactions, blockNumber *big.Int, minTimestamp, maxTimestamp uint64, revertingTxHashes []common.Hash) error {
	for _, tx := range txs {
		*mevPool = append(*mevPool, tx.Hash())
	}
	return nil
}

type testSigner struct{}

func (signer testSigner) Address() common.Address {
	return common.Address{}
}

func (signer testSigner) SignTx(tx *types.Transaction) (*types.Transaction, error) {
	return tx, nil
}

func newTestUserOpPool() *UserOpPool {
	statedb, _ := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	chain := testBlockChainWithContext{testBlockChain{1000000000, statedb, new(event.Feed)}}

	return &UserOpPool{
		config: UserOpPoolConfig{},

		initDoneCh:  make(chan struct{}),
		chainHeadCh: make(chan ChainHeadEvent),

		chainConfig: params.TestChainConfig,
		chain:       &chain,
		mevPool:     &testMevPool{},
		signer:      &testSigner{},

		bySender:     make(map[common.Address]*types.SimulatedUserOp),
		byEntryPoint: make(map[common.Address][]*types.SimulatedUserOp),
	}
}

type testAAContracts struct {
	entryPointAddr       common.Address
	simpleWalletAddr     common.Address
	singletonFactoryAddr common.Address
	testCounterAddr      common.Address
	tokenPaymasterAddr   common.Address
}

func deployContract(chain blockChainWithContext, deploymentCode []byte) (common.Address, error) {
	block := chain.CurrentBlock()
	statedb, _ := chain.StateAt(block.Hash())
	blockCtx := NewEVMBlockContext(block.Header(), chain, &common.Address{})
	txCtx := vm.TxContext{Origin: common.Address{}, GasPrice: common.Big0}
	evm := vm.NewEVM(blockCtx, txCtx, statedb, params.TestChainConfig, vm.Config{})
	_, addr, _, err := evm.Create(vm.AccountRef(common.Address{}), deploymentCode, chain.CurrentBlock().GasLimit(), common.Big0)
	return addr, err
}

func deployAAContracts(chain blockChainWithContext, walletUser common.Address) (*testAAContracts, error) {
	var (
		contracts testAAContracts
		err       error
	)

	if deploy, err := aatestcontracts.SingletonFactory.PackDeploy(); err == nil {
		contracts.singletonFactoryAddr, err = deployContract(chain, deploy)
	}
	if deploy, err := entrypoint.Contract.PackDeploy(contracts.singletonFactoryAddr, common.Big0, common.Big0); err == nil {
		contracts.entryPointAddr, err = deployContract(chain, deploy)
	}
	if deploy, err := aatestcontracts.SimpleWallet.PackDeploy(contracts.entryPointAddr, walletUser); err == nil {
		contracts.simpleWalletAddr, err = deployContract(chain, deploy)
	}
	if deploy, err := aatestcontracts.TestCounter.PackDeploy(); err == nil {
		contracts.testCounterAddr, err = deployContract(chain, deploy)
	}
	if deploy, err := aatestcontracts.TokenPaymaster.PackDeploy("symbol", contracts.entryPointAddr); err == nil {
		contracts.tokenPaymasterAddr, err = deployContract(chain, deploy)
	}

	return &contracts, err
}

func newUserOpThatIncCounter(contracts *testAAContracts) (types.UserOp, error) {
	countCallData, err := aatestcontracts.TestCounter.PackCount()
	if err != nil {
		return types.UserOp{}, err
	}

	execFromEntryPointCallData, err := aatestcontracts.SimpleWallet.PackExecFromEntryPoint(contracts.testCounterAddr, common.Big0, countCallData)
	if err != nil {
		return types.UserOp{}, err
	}

	return types.UserOp{
		Signature: []byte{},

		PreVerificationGas: uint64(100000),
		VerificationGas:    uint64(1000000),
		CallGas:            uint64(1000000),
		GasTipCap:          *common.Big0,
		GasFeeCap:          *common.Big0,

		InitCode:      []byte{},
		Sender:        contracts.simpleWalletAddr,
		CallData:      execFromEntryPointCallData,
		Paymaster:     common.Address{},
		PaymasterData: []byte{},
	}, nil
}

func TestSimulateUserOp(t *testing.T) {
	pool := newTestUserOpPool()
	contracts, err := deployAAContracts(pool.chain, common.Address{})
	if err != nil {
		t.Fatalf("Couldn't deploy account abstraction test contracts: {%s}", err)
	}
	userOp, err := newUserOpThatIncCounter(contracts)
	if err != nil {
		t.Fatalf("Error creating test UserOp: {%s}", err)
	}
	_, _, simErr := pool.SimulateUserOp(&userOp, &contracts.entryPointAddr, pool.chain.CurrentBlock())
	if simErr != nil {
		t.Fatalf("Simulation of user operation failed: {%s}", simErr)
	}
}
