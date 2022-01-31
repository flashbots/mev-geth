package core

import (
	"crypto/ecdsa"
	"fmt"
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
	// tokenPaymasterAddr   common.Address
}

func deployContract(chain blockChainWithContext, deploymentCode []byte, balance *big.Int) (common.Address, error) {
	block := chain.CurrentBlock()
	statedb, _ := chain.StateAt(block.Hash())
	blockCtx := NewEVMBlockContext(block.Header(), chain, &common.Address{})
	txCtx := vm.TxContext{Origin: common.Address{}, GasPrice: common.Big0}
	evm := vm.NewEVM(blockCtx, txCtx, statedb, params.TestChainConfig, vm.Config{})
	ret, addr, _, vmErr := evm.Create(vm.AccountRef(common.Address{}), deploymentCode, chain.CurrentBlock().GasLimit(), common.Big0)
	if vmErr == nil {
		statedb.SetBalance(addr, balance)
		fmt.Println("Deployed to:", addr)
	} else if vmErr == vm.ErrExecutionReverted {
		if revert, err := abi.UnpackRevert(ret); err == nil {
			fmt.Printf("-> You got reverted while deploying: %s\n", revert)
		} else {
			customErr := entrypoint.Contract.Abi.Errors["FailedOp"]
			if args, err := customErr.Unpack(ret); err == nil {
				fmt.Printf("-> You got reverted while deploying: %s - %v\n", customErr.Sig, args)
			} else {
				fmt.Printf("-> You got reverted while deploying (couldn't unpack revert)\n")
			}
		}
	}

	return addr, vmErr
}

func deployAAContracts(chain blockChainWithContext, userAddr common.Address) (*testAAContracts, error) {
	var contracts testAAContracts

	if deploy, err := aatestcontracts.SingletonFactory.PackDeploy(); err == nil {
		fmt.Println("Deploying SingletonFactory")
		if contracts.singletonFactoryAddr, err = deployContract(chain, deploy, common.Big0); err != nil {
			return &contracts, err
		}
	} else {
		return &contracts, err
	}

	if deploy, err := entrypoint.Contract.PackDeploy(contracts.singletonFactoryAddr, common.Big0, 0); err == nil {
		fmt.Println("Deploying EntryPoint")
		if contracts.entryPointAddr, err = deployContract(chain, deploy, common.Big0); err != nil {
			return &contracts, err
		}
	} else {
		return &contracts, err
	}

	if deploy, err := aatestcontracts.SimpleWallet.PackDeploy(contracts.entryPointAddr, userAddr); err == nil {
		fmt.Println("Deploying SimpleWallet")
		if contracts.simpleWalletAddr, err = deployContract(chain, deploy, new(big.Int).Mul(common.Big1, big.NewInt(params.Ether))); err != nil {
			return &contracts, err
		}
	} else {
		return &contracts, err
	}

	if deploy, err := aatestcontracts.TestCounter.PackDeploy(); err == nil {
		fmt.Println("Deploying TestCounter")
		if contracts.testCounterAddr, err = deployContract(chain, deploy, common.Big0); err != nil {
			return &contracts, err
		}
	} else {
		return &contracts, err
	}
	//
	// if deploy, err := aatestcontracts.TokenPaymaster.PackDeploy("symbol", contracts.entryPointAddr); err == nil {
	// 	fmt.Println("Deploying TokenPaymaster")
	// 	if contracts.tokenPaymasterAddr, err = deployContract(chain, deploy, common.Big0); err != nil {
	// 		return &contracts, err
	// 	}
	// } else {
	// 	return &contracts, err
	// }

	return &contracts, nil
}

func signUserOperation(userOp *types.UserOp, entryPointAddr common.Address, chainId *big.Int, key *ecdsa.PrivateKey) (*types.UserOp, error) {
	userOpAbi := abi.Arguments{entrypoint.Contract.Abi.Methods["simulateValidation"].Inputs[0]}
	packedUserOp, err := userOpAbi.Pack(userOp.ToAbi())
	if err != nil {
		fmt.Printf("-> Couldn't pack user op for signing: %v\n", err)
		return userOp, err
	}
	packedUserOp = packedUserOp[32 : len(packedUserOp)-32]
	hashedUserOp := crypto.Keccak256Hash(packedUserOp)

	bytes32Type, _ := abi.NewType("bytes32", "", nil)
	addressType, _ := abi.NewType("address", "", nil)
	uint256Type, _ := abi.NewType("uint256", "", nil)
	requestIdAbi := abi.Arguments{
		abi.Argument{Name: "userOp", Type: bytes32Type},
		abi.Argument{Name: "entryPoint", Type: addressType},
		abi.Argument{Name: "chainId", Type: uint256Type},
	}
	requestId, err := requestIdAbi.Pack(hashedUserOp, entryPointAddr, chainId)
	if err != nil {
		return nil, err
	}
	hashedRequestId := crypto.Keccak256Hash(requestId)

	msg := accounts.TextHash(hashedRequestId.Bytes())
	userOp.Signature, err = crypto.Sign(msg, key)

	// geth generates signature where v value is 0 or 1
	// whereas SimpleWallet recovery mechanism expects
	// v to be 27 or 28
	userOp.Signature[len(userOp.Signature)-1] += 27

	fmt.Printf("Data to sign: %s\n", hexutil.Encode(msg))
	fmt.Printf("Signature: %s\n", hexutil.Encode(userOp.Signature))

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
		GasTipCap:          *common.Big0,
		GasFeeCap:          *common.Big256,

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
	fmt.Printf("-> Signing user op with address %s\n", userAddr)

	pool := newTestUserOpPool()
	contracts, err := deployAAContracts(pool.chain, userAddr)
	if err != nil {
		t.Fatalf("Couldn't deploy account abstraction test contracts: %s", err)
	}
	userOp, err := newUserOpThatIncCounter(contracts)
	if err != nil {
		t.Fatalf("Error creating test UserOp: %s", err)
	}
	userOp, err = signUserOperation(userOp, contracts.entryPointAddr, pool.chainConfig.ChainID, key)
	if err != nil {
		t.Fatalf("Error signing test UserOp: %s", err)
	}

	_, _, simErr := pool.SimulateUserOp(userOp, &contracts.entryPointAddr, pool.chain.CurrentBlock())
	if simErr != nil {
		t.Fatalf("Simulation of user operation failed: %s", simErr)
	}
}
