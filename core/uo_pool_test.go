package core

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/contracts/aatestcontracts"
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
	simpleWalletAddr     common.Address
	singletonFactoryAddr common.Address
	testCounterAddr      common.Address
	tokenPaymasterAddr   common.Address
}

func deployAATestContract(chain blockChainWithContext, deploymentCode []byte) (common.Address, error) {
	block := chain.CurrentBlock()
	statedb, _ := chain.StateAt(block.Hash())
	blockCtx := NewEVMBlockContext(block.Header(), chain, &common.Address{})
	txCtx := vm.TxContext{Origin: common.Address{}, GasPrice: common.Big0}
	evm := vm.NewEVM(blockCtx, txCtx, statedb, params.TestChainConfig, vm.Config{})
	_, addr, _, err := evm.Create(vm.AccountRef(common.Address{}), deploymentCode, chain.CurrentBlock().GasLimit(), common.Big0)
	return addr, err
}

func deployAllAATestContracts(chain blockChainWithContext) (*testAAContracts, error) {
	simpleWalletDeploy, err := aatestcontracts.SimpleWallet.PackDeploy()
	singletonFactoryDeploy, err := aatestcontracts.SimpleWallet.PackDeploy()
	testCounterDeploy, err := aatestcontracts.SimpleWallet.PackDeploy()
	tokenPaymasterDeploy, err := aatestcontracts.SimpleWallet.PackDeploy()
	if err != nil {
		return nil, err
	}

	simpleWalletAddr, err := deployAATestContract(chain, simpleWalletDeploy)
	singletonFactoryAddr, err := deployAATestContract(chain, singletonFactoryDeploy)
	testCounterAddr, err := deployAATestContract(chain, testCounterDeploy)
	tokenPaymasterAddr, err := deployAATestContract(chain, tokenPaymasterDeploy)
	return &testAAContracts{
		simpleWalletAddr:     simpleWalletAddr,
		singletonFactoryAddr: singletonFactoryAddr,
		testCounterAddr:      testCounterAddr,
		tokenPaymasterAddr:   tokenPaymasterAddr,
	}, err
}

func newUserOpThatIncCounter(walletAddr common.Address) types.UserOp {
	return types.UserOp{
		Signature: []byte{},

		PreVerificationGas: uint64(100000),
		VerificationGas:    uint64(1000000),
		CallGas:            uint64(1000000),
		GasTipCap:          *common.Big0,
		GasFeeCap:          *common.Big0,

		InitCode:      []byte{},
		Sender:        walletAddr,
		CallData:      []byte{},
		Paymaster:     common.Address{},
		PaymasterData: []byte{},
	}
}

func TestSimulateUserOp(t *testing.T) {
	pool := newTestUserOpPool()
	contracts, err := deployAllAATestContracts(pool.chain)
	if err != nil {
		t.Fatalf("Couldn't deploy test account abstraction contracts")
	}

	_ = contracts

}
