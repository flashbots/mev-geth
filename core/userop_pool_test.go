package core

import (
	"crypto/ecdsa"
	"math/big"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/contracts/aatestcontracts"
	"github.com/ethereum/go-ethereum/contracts/entrypoint"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
)

// TODO: REFACTOR: I don't like having references to testing.T on all helper methods

type testBlockChainWithContext struct {
	testBlockChain
}

func (bc *testBlockChainWithContext) CurrentBlock() *types.Block {
	return types.NewBlock(&types.Header{
		GasLimit: atomic.LoadUint64(&bc.gasLimit),
		BaseFee:  common.Big256,
	}, nil, nil, nil, trie.NewStackTrie(nil))
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

		byEntryPoint: make(map[common.Address][]*types.SimulatedUserOp),
	}
}

type testAAContracts struct {
	entryPointAddr       common.Address
	simpleWalletAddr     common.Address
	singletonFactoryAddr common.Address
	testCounterAddr      common.Address
	// tokenPaymasterAddr   common.Address
}

func deployContract(t *testing.T, chain blockChainWithContext, deploymentCode []byte, balance *big.Int) (common.Address, error) {
	block := chain.CurrentBlock()
	statedb, _ := chain.StateAt(block.Hash())
	blockCtx := NewEVMBlockContext(block.Header(), chain, &common.Address{})
	txCtx := vm.TxContext{Origin: common.Address{}, GasPrice: common.Big0}
	evm := vm.NewEVM(blockCtx, txCtx, statedb, params.TestChainConfig, vm.Config{})
	ret, addr, _, vmErr := evm.Create(vm.AccountRef(common.Address{}), deploymentCode, chain.CurrentBlock().GasLimit(), common.Big0)
	if vmErr == nil {
		statedb.SetBalance(addr, balance)
		t.Log("Deployed to:", addr)
	} else if vmErr == vm.ErrExecutionReverted {
		if revert, err := abi.UnpackRevert(ret); err == nil {
			t.Logf("You got reverted while deploying: %s\n", revert)
		} else {
			t.Logf("You got reverted while deploying (couldn't unpack revert)\n")
		}
	}

	return addr, vmErr
}

func deployAAContracts(t *testing.T, chain blockChainWithContext, userAddr common.Address) (*testAAContracts, error) {
	var contracts testAAContracts

	if deploy, err := aatestcontracts.SingletonFactory.PackDeploy(); err == nil {
		t.Log("Deploying SingletonFactory")
		if contracts.singletonFactoryAddr, err = deployContract(t, chain, deploy, common.Big0); err != nil {
			return &contracts, err
		}
	} else {
		return &contracts, err
	}

	if deploy, err := entrypoint.Contract.PackDeploy(contracts.singletonFactoryAddr, common.Big0, 0); err == nil {
		t.Log("Deploying EntryPoint")
		if contracts.entryPointAddr, err = deployContract(t, chain, deploy, common.Big0); err != nil {
			return &contracts, err
		}
	} else {
		return &contracts, err
	}

	if deploy, err := aatestcontracts.SimpleWallet.PackDeploy(contracts.entryPointAddr, userAddr); err == nil {
		t.Log("Deploying SimpleWallet")
		if contracts.simpleWalletAddr, err = deployContract(t, chain, deploy, new(big.Int).Mul(common.Big1, big.NewInt(params.Ether))); err != nil {
			return &contracts, err
		}
	} else {
		return &contracts, err
	}

	if deploy, err := aatestcontracts.TestCounter.PackDeploy(); err == nil {
		t.Log("Deploying TestCounter")
		if contracts.testCounterAddr, err = deployContract(t, chain, deploy, common.Big0); err != nil {
			return &contracts, err
		}
	} else {
		return &contracts, err
	}
	//
	// if deploy, err := aatestcontracts.TokenPaymaster.PackDeploy("symbol", contracts.entryPointAddr); err == nil {
	// 	t.Log("Deploying TokenPaymaster")
	// 	if contracts.tokenPaymasterAddr, err = deployContract(chain, deploy, common.Big0); err != nil {
	// 		return &contracts, err
	// 	}
	// } else {
	// 	return &contracts, err
	// }

	return &contracts, nil
}

func signUserOperation(t *testing.T, userOp *types.UserOp, entryPointAddr common.Address, chainId *big.Int, key *ecdsa.PrivateKey) (*types.UserOp, error) {
	requestId, err := userOp.ToAbi().RequestId(entryPointAddr, chainId)
	if err != nil {
		return userOp, err
	}
	msg := accounts.TextHash(requestId.Bytes())
	userOp.Signature, err = crypto.Sign(msg, key)

	// geth generates signature where v value is 0 or 1
	// whereas SimpleWallet recovery mechanism expects
	// v to be 27 or 28
	userOp.Signature[len(userOp.Signature)-1] += 27

	t.Logf("Data to sign: %s\n", hexutil.Encode(msg))
	t.Logf("Signature: %s\n", hexutil.Encode(userOp.Signature))

	return userOp, err
}

func newUserOpThatIncCounter(contracts *testAAContracts) (*types.UserOp, error) {
	countCallData, err := aatestcontracts.TestCounter.PackCount()
	if err != nil {
		return nil, err
	}

	execFromEntryPointCallData, err := aatestcontracts.SimpleWallet.PackExecFromEntryPoint(contracts.testCounterAddr, common.Big0, countCallData)
	if err != nil {
		return nil, err
	}

	return &types.UserOp{
		Nonce:              uint64(0),
		PreVerificationGas: uint64(100000),
		VerificationGas:    uint64(1000000),
		CallGas:            uint64(1000000),
		GasTipCap:          common.Big0,
		GasFeeCap:          common.Big256,

		InitCode:      []byte{},
		Sender:        contracts.simpleWalletAddr,
		CallData:      execFromEntryPointCallData,
		Paymaster:     common.Address{},
		PaymasterData: []byte{},
	}, err
}

func TestSimulateUserOp(t *testing.T) {
	key, err := crypto.HexToECDSA("5217abe399807b572957df221026f1c05a8594286f279e20e4854f26f2404531")
	if err != nil {
		t.Fatalf("Couldn't generate private key to simulate user: %s", err)
	}
	userAddr := crypto.PubkeyToAddress(key.PublicKey)
	t.Logf("Signing user op with address %s\n", userAddr)

	pool := newTestUserOpPool()
	contracts, err := deployAAContracts(t, pool.chain, userAddr)
	if err != nil {
		t.Fatalf("Couldn't deploy account abstraction test contracts: %s", err)
	}
	userOp, err := newUserOpThatIncCounter(contracts)
	if err != nil {
		t.Fatalf("Error creating test UserOp: %s", err)
	}
	userOp, err = signUserOperation(t, userOp, contracts.entryPointAddr, pool.chainConfig.ChainID, key)
	if err != nil {
		t.Fatalf("Error signing test UserOp: %s", err)
	}

	_, simErr := pool.simulateUserOp(userOp, &contracts.entryPointAddr, pool.chain.CurrentBlock())
	if simErr != nil {
		t.Fatalf("Simulation of user operation failed: %s", simErr)
	}
}

func TestUserOperationReqId(t *testing.T) {
	entryPointAddr := common.HexToAddress("0x90f3E1105E63C877bF9587DE5388C23Cdb702c6B")
	chainId := big.NewInt(5)

	args := map[*types.UserOp]common.Hash{
		{
			Sender:             common.HexToAddress("0x05c022028ef3e2c61b3babe0fbc8f658bc4b431f"),
			Nonce:              5,
			CallData:           common.FromHex("0x2b311337000000000000000000000000000000000000000000000000000000000000012c"),
			InitCode:           []byte{},
			CallGas:            21000,
			VerificationGas:    21000,
			PreVerificationGas: 21000,
			GasFeeCap:          big.NewInt(2100000000),
			GasTipCap:          big.NewInt(2100000000),
			Paymaster:          common.Address{},
			PaymasterData:      []byte{},
			Signature:          []byte{},
		}: common.HexToHash("0x9f5d37eb5cc7b0707b2898b1da01fa7aac806a18d531b17a981994bc512cbfc8"),
		{
			Sender:             common.HexToAddress("0x65f1326ef62E7b63B2EdF41840E37eB2a0F97515"),
			Nonce:              7,
			CallData:           common.Hex2Bytes("80c5c7d000000000000000000000000017e4493e5dc3e0bafdb68147cf15f52f669ef91d000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000600000000000000000000000000000000000000000000000000000000000000004278ddd3c00000000000000000000000000000000000000000000000000000000"),
			InitCode:           []byte{},
			CallGas:            29129,
			VerificationGas:    100000,
			PreVerificationGas: 21000,
			GasFeeCap:          big.NewInt(1000000007),
			GasTipCap:          big.NewInt(1000000000),
			Paymaster:          common.Address{},
			PaymasterData:      []byte{},
			Signature:          common.Hex2Bytes("e4ef96c1ebffdae061838b79a0ba2b0289083099dc4d576a7ed0c61c80ed893273ba806a581c72be9e550611defe0bf490f198061b8aa63dd6acfc0b620e0c871c"),
		}: common.HexToHash("0x87c3605deda77b02b78e62157309985d94531cf7fbb13992c602c8555bece921"),
	}

	for userOp, expected := range args {
		t.Run("", func(t *testing.T) {
			requestId, err := userOp.ToAbi().RequestId(entryPointAddr, chainId)
			if err != nil {
				t.Fatalf("Couldn't compute requestId: %s", err)
			}

			if requestId != expected {
				t.Fatalf("RequestId: expected -> %v actual -> %v", expected, requestId)
			}
		})
	}
}
