package core

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/contracts/entrypoint"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

const (
	overheadGas = uint64(0)
)

var (
	// ErrSignerAccountNotFound is returned if the provided signer
	// doesn't have an account registered under AccountManager
	ErrSignerAccountNotFound = errors.New("The provided signer address doesn't have an account associated")

	// ErrSenderUserOperationInPool is returned if the pool already contains a
	// user operation with the given sender.
	ErrSenderUserOpInPool = errors.New("Sender already has a user operation in the pool")

	// ErrUserOpSimulationFailed is returned if the simulation of the
	// user operation fails, and prevents it from being added to the pool
	ErrUserOpSimulationFailed = errors.New("Simulation of user operation failed before adding to pool")

	// ErrUserOpSimulationFailed is returned if the simulation of the
	// user operation fails right before being bundled, and prevents it from being included in a tx
	ErrUserOpReSimulationFailed = errors.New("Simulation of user operation failed before adding to pool")
)

type blockChainWithContext interface {
	blockChain
	ChainContext
}

type mevPool interface {
	AddMevBundle(
		txs types.Transactions, blockNumber *big.Int,
		minTimestamp, maxTimestamp uint64,
		revertingTxHashes []common.Hash,
	) error
}

type signer interface {
	Address() common.Address
	SignTx(tx *types.Transaction) (*types.Transaction, error)
}

type walletSigner struct {
	wallet  accounts.Wallet
	account accounts.Account
	chainId *big.Int
}

func (signer walletSigner) Address() common.Address {
	return signer.account.Address
}

func (signer walletSigner) SignTx(tx *types.Transaction) (*types.Transaction, error) {
	return signer.wallet.SignTx(signer.account, tx, signer.chainId)
}

type UserOpPoolConfig struct {
	signerAddr common.Address
	maxBundles uint
}

type UserOpPool struct {
	mu sync.RWMutex
	wg sync.WaitGroup

	config UserOpPoolConfig

	initDoneCh   chan struct{}
	chainHeadCh  chan ChainHeadEvent
	chainHeadSub event.Subscription
	bundleTicker *time.Ticker

	chainConfig *params.ChainConfig
	chain       blockChainWithContext
	mevPool     mevPool
	signer      signer

	byEntryPoint map[common.Address][]*types.SimulatedUserOp
}

func NewUserOpPool(config UserOpPoolConfig, chainConfig *params.ChainConfig, chain blockChainWithContext, mevPool mevPool, accountManager *accounts.Manager) (*UserOpPool, error) {
	var (
		wallet  accounts.Wallet
		account accounts.Account
	)
	if (config.signerAddr == common.Address{}) {
		if wallets := accountManager.Wallets(); len(wallets) > 0 {
			if accounts := wallets[0].Accounts(); len(accounts) > 0 {
				wallet = wallets[0]
				account = accounts[0]
				config.signerAddr = account.Address
			} else {
				return nil, ErrSignerAccountNotFound
			}
		} else {
			return nil, ErrSignerAccountNotFound
		}
	} else {
		account = accounts.Account{Address: config.signerAddr}
		if signerWallet, err := accountManager.Find(account); err == nil {
			wallet = signerWallet
		} else {
			return nil, err
		}
	}

	// if config.maxBundles == 0 {
	// 	config.maxBundles = 1
	// }

	pool := &UserOpPool{
		config: config,

		initDoneCh:  make(chan struct{}),
		chainHeadCh: make(chan ChainHeadEvent),

		chainConfig: chainConfig,
		chain:       chain,
		mevPool:     mevPool,
		signer:      &walletSigner{wallet, account, chainConfig.ChainID},

		byEntryPoint: make(map[common.Address][]*types.SimulatedUserOp),
	}

	pool.chainHeadSub = pool.chain.SubscribeChainHeadEvent(pool.chainHeadCh)
	pool.wg.Add(1)
	go pool.mainLoop()

	return pool, nil
}

// mainLoop is the userOp pool's main event loop, waiting for and reacting to
// outside blockchain events
func (pool *UserOpPool) mainLoop() {
	defer pool.wg.Done()

	// let tests know we are finished initializating
	close(pool.initDoneCh)

	for {
		select {
		case ev := <-pool.chainHeadCh:
			if ev.Block != nil {
				for entryPointAddr := range pool.byEntryPoint {
					go pool.bundleByEntryPoint(ev.Block, &entryPointAddr)
				}
			}

		case <-pool.chainHeadSub.Err():
			// close(pool.reorgShutdownCh)
			return
		}
	}
}

func (pool *UserOpPool) Stop() {
	// Unsubscribe subscriptions registered from blockchain
	pool.chainHeadSub.Unsubscribe()
	pool.bundleTicker.Stop()

	// Wait for workers to stop
	pool.wg.Wait()

	log.Info("UserOp pool stopped")
}

// AddUserOp adds a user operation to the pool
func (pool *UserOpPool) AddUserOp(userOp *types.UserOp, entryPointAddr *common.Address) error {
	// TODO: Sanity checks here, check nethermind

	simmed, err := pool.simulateUserOp(userOp, entryPointAddr)
	if err != nil {
		log.Warn("Discarding user operation because it didn't pass simulation")
		return err
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	log.Warn("Adding user operation to the pool")
	pool.byEntryPoint[*entryPointAddr] = append(pool.byEntryPoint[*entryPointAddr], simmed)
	return nil
}

func (pool *UserOpPool) SimulateUserOp(userOp *types.UserOp, entryPointAddr *common.Address, block *types.Block) (types.AccessList, uint64, error) {
	state, err := pool.chain.StateAt(block.Root())
	if err != nil {
		return nil, 0, err
	}

	blockCtx := NewEVMBlockContext(block.Header(), pool.chain, &common.Address{})
	txCtx := vm.TxContext{
		Origin:   common.Address{},
		GasPrice: block.BaseFee(),
	}
	tracer := vm.NewUserOperationTracer(*entryPointAddr, userOp.Paymaster, userOp.Sender)
	evm := vm.NewEVM(blockCtx, txCtx, state, pool.chainConfig, vm.Config{
		Tracer: tracer,
		Debug:  true,
	})

	data, err := entrypoint.Contract.PackSimulateValidation(userOp)
	if err != nil {
		return nil, 0, err
	}

	userOpGas := userOp.PreVerificationGas + userOp.VerificationGas
	res, gasLeft, vmErr := evm.Call(vm.AccountRef(common.Address{}), *entryPointAddr, data, userOpGas, common.Big0)
	gasUsed := userOpGas - gasLeft
	if vmErr != nil {
		if errors.Is(vmErr, vm.ErrExecutionReverted) {
			if revert, err := abi.UnpackRevert(res); err == nil {
				vmErr = fmt.Errorf("%w %s", err, revert)
			} else {
				customErr := entrypoint.Contract.Abi.Errors["FailedOp"]
				if args, err := customErr.Unpack(res); err == nil {
					vmErr = fmt.Errorf("%w %s with args %v", err, customErr.Sig, args)
				} else {
					vmErr = fmt.Errorf("%w %s", err, customErr.Sig)
				}
			}
		}
		return nil, gasUsed, vmErr
	}

	if tracerErr := tracer.Err(); tracerErr != nil {
		return nil, gasUsed, tracerErr
	}

	return tracer.AccessList(), gasUsed, nil
}

func (pool *UserOpPool) simulateUserOp(userOp *types.UserOp, entryPointAddr *common.Address) (*types.SimulatedUserOp, error) {
	block := pool.chain.CurrentBlock()
	accessList, gasUsed, simErr := pool.SimulateUserOp(userOp, entryPointAddr, block)
	if simErr != nil {
		return nil, simErr
	}

	return &types.SimulatedUserOp{
		UserOp:     userOp,
		AccessList: accessList,
		GasUsed:    gasUsed,
	}, nil
}

func (pool *UserOpPool) reSimulateUserOp(simmedUserOp *types.SimulatedUserOp, entryPointAddr *common.Address) error {
	block := pool.chain.CurrentBlock()
	accessList, gasUsed, simErr := pool.SimulateUserOp(simmedUserOp.UserOp, entryPointAddr, block)
	if simErr != nil {
		return simErr
	}

	if !accessList.Equals(simmedUserOp.AccessList) || gasUsed != simmedUserOp.GasUsed { // TODO: remove gasUsed
		return ErrUserOpReSimulationFailed
	}

	return nil
}

// bundleUserOperations checks the user operation pool and bundles
// them into a transaction to later be wrapped in an MevBundle
func (pool *UserOpPool) bundleByEntryPoint(head *types.Block, entryPointAddr *common.Address) error {
	gasLimit := head.GasLimit() // TODO: check this
	maxBundles := pool.config.maxBundles

	pool.mu.Lock()
	defer pool.mu.Unlock()

	userOpList := pool.byEntryPoint[*entryPointAddr]
	// TODO: maybe this shouldn't be sorted here
	// use some more efficient data structure
	sort.Slice(userOpList, func(i, j int) bool {
		return userOpList[i].GasTipCap.Cmp(&userOpList[j].GasTipCap) < 0
	})

	userOps := []*types.UserOp{}
	totalGasUsed := uint64(0)
	totalAccessList := types.AccessList{}

	bundlesCreated := uint(0)
	for i, simmed := range userOpList {
		if err := pool.reSimulateUserOp(simmed, entryPointAddr); err != nil {
			log.Warn("Drop user operation because it didn't pass resimulation")
			pool.byEntryPoint[*entryPointAddr] = append(userOpList[:i], userOpList[i+1:]...)
			continue
		}

		if totalGasUsed+simmed.GasUsed > gasLimit {
			// TODO: do this earlier, before resimulation
			continue
		}

		if totalAccessList.Overlaps(simmed.AccessList) {
			continue
		}

		totalGasUsed += simmed.PreVerificationGas + simmed.VerificationGas + simmed.CallGas
		// totalAccessList.Extend(simmed.AccessList) // TODO
		userOps = append(userOps, simmed.UserOp)
		pool.byEntryPoint[*entryPointAddr] = append(userOpList[:i], userOpList[i+1:]...) // TODO: this should be done after inclusion in a block

		// TODO
		// Not sure if this is the correct fee for the tx
		// Maybe gasFee should be the sum of all bundled userOp gasFee
		gasFee := misc.CalcBaseFee(pool.chainConfig, head.Header())
		signedTx, err := pool.bundleIntoTx(userOps, entryPointAddr, gasFee)
		if err != nil {
			return err
		}

		log.Warn("Add bundled transaction to mev bundle pool")
		nextBlockNumber := new(big.Int).Add(head.Number(), common.Big1)
		if err := pool.mevPool.AddMevBundle(types.Transactions{signedTx}, nextBlockNumber, 0, 0, nil); err != nil {
			return err
		}

		bundlesCreated += 1
		if bundlesCreated >= maxBundles {
			break
		}
	}

	return nil
}

// bundleUserOperations checks the user operation pool and bundles
// them into a transaction to later be wrapped in an MevBundle
func (pool *UserOpPool) bundleIntoTx(userOps []*types.UserOp, entryPointAddr *common.Address, gasFee *big.Int) (*types.Transaction, error) {
	// TODO: eth_estimateGas: gas
	var (
		err  error
		data []byte
	)
	if len(userOps) == 1 {
		data, err = entrypoint.Contract.PackHandleOp(userOps[0], pool.signer.Address())
	} else {
		data, err = entrypoint.Contract.PackHandleOps(userOps, pool.signer.Address())
	}
	if err != nil {
		return nil, err
	}

	gas := uint64(0)
	for _, uo := range userOps {
		gas += uo.VerificationGas + uo.CallGas + overheadGas
	}

	chainId := pool.chainConfig.ChainID
	tx := types.NewTx(&types.DynamicFeeTx{
		To:        entryPointAddr,
		ChainID:   chainId,
		Gas:       gas,
		GasFeeCap: gasFee,
		GasTipCap: common.Big0,
		Value:     common.Big0,
		Data:      data,
	})

	return pool.signer.SignTx(tx)
}

func (pool *UserOpPool) userOpsByGasLimit(gasLimit uint64, entryPointAddr *common.Address) []*types.UserOp {
	pool.mu.RLock()
	defer pool.mu.Unlock()

	userOpList := pool.byEntryPoint[*entryPointAddr]
	sort.Slice(userOpList, func(i, j int) bool {
		return userOpList[i].GasTipCap.Cmp(&userOpList[j].GasTipCap) < 0
	})

	userOps := []*types.UserOp{}
	totalGasUsed := uint64(0)
	totalAccessList := types.AccessList{}

	for i, simmed := range userOpList {
		if err := pool.reSimulateUserOp(simmed, entryPointAddr); err != nil {
			log.Warn("Dropped user operation because it didn't pass resimulation")
			pool.byEntryPoint[*entryPointAddr] = append(userOpList[:i], userOpList[i+1:]...)
			continue
		}

		if totalGasUsed+simmed.GasUsed > gasLimit {
			continue
		}

		if totalAccessList.Overlaps(simmed.AccessList) {
			continue
		}

		totalGasUsed += simmed.GasUsed
		totalAccessList.Extend(simmed.AccessList)
		userOps = append(userOps, simmed.UserOp)
	}

	return userOps
}
