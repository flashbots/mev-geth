package core

import (
	"errors"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/contracts/entrypoint"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
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

type UserOpPoolConfig struct {
	signerAddr common.Address
	maxBundles uint
}

type UserOpPool struct {
	mu sync.RWMutex
	wg sync.WaitGroup

	config UserOpPoolConfig

	chainHeadCh  chan ChainHeadEvent
	chainHeadSub event.Subscription
	bundleTicker *time.Ticker

	txPool        *TxPool
	signerWallet  accounts.Wallet
	signerAccount accounts.Account

	bySender     map[common.Address]*types.SimulatedUserOp
	byEntryPoint map[common.Address][]*types.SimulatedUserOp
}

func NewUserOpPool(config UserOpPoolConfig, txPool *TxPool, accountManager *accounts.Manager) (*UserOpPool, error) {
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

	if config.maxBundles == 0 {
		config.maxBundles = 1
	}

	pool := &UserOpPool{
		config: config,

		chainHeadCh: make(chan ChainHeadEvent),

		txPool:        txPool,
		signerWallet:  wallet,
		signerAccount: account,

		bySender:     make(map[common.Address]*types.SimulatedUserOp),
		byEntryPoint: make(map[common.Address][]*types.SimulatedUserOp),
	}

	pool.chainHeadSub = pool.txPool.chain.SubscribeChainHeadEvent(pool.chainHeadCh)
	pool.bundleTicker = time.NewTicker(10)
	pool.wg.Add(1)
	go pool.mainLoop()

	return pool, nil
}

// mainLoop is the userOp pool's main event loop, waiting for and reacting to
// outside blockchain events
func (pool *UserOpPool) mainLoop() {
	defer pool.wg.Done()

	for {
		select {
		case ev := <-pool.chainHeadCh:
			if ev.Block != nil {
				for entryPointAddr := range pool.byEntryPoint {
					go pool.bundleByEntryPoint(ev.Block, &entryPointAddr)
				}
			}

		case <-pool.bundleTicker.C:
			head := pool.txPool.chain.CurrentBlock()
			for entryPointAddr := range pool.byEntryPoint {
				go pool.bundleByEntryPoint(head, &entryPointAddr)
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
	if _, ok := pool.bySender[userOp.Sender]; ok {
		log.Warn("Discarding user operation as there is already another one in the pool with same sender")
		return ErrSenderUserOpInPool
	}

	simmed, err := pool.SimulateUserOp(userOp, entryPointAddr)
	if err != nil {
		log.Warn("Discarding user operation because it didn't pass simulation")
		return err
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	log.Warn("Adding user operation to the pool")
	pool.bySender[simmed.Sender] = simmed
	pool.byEntryPoint[*entryPointAddr] = append(pool.byEntryPoint[*entryPointAddr], simmed)
	return nil
}

func (pool *UserOpPool) newSimulator() *UserOpSimulator {
	block := pool.txPool.chain.CurrentBlock()
	return &UserOpSimulator{
		BlockNumber:     block.Number(),
		BlockDifficulty: block.Difficulty(),
		GasLimit:        block.GasLimit(),
		BaseFee:         block.BaseFee(),
		GasPrice:        pool.txPool.GasPrice(),
		StateDB:         pool.txPool.currentState.Copy(),
		ChainConfig:     pool.txPool.chainconfig,
	}
}

func (pool *UserOpPool) SimulateUserOp(userOp *types.UserOp, entryPointAddr *common.Address) (*types.SimulatedUserOp, error) {
	simulator := pool.newSimulator()

	accessList, gasUsed, simErr := simulator.SimulateUserOp(userOp, entryPointAddr)
	if simErr != nil {
		return nil, simErr
	}

	return &types.SimulatedUserOp{
		UserOp:     userOp,
		AccessList: accessList,
		GasUsed:    gasUsed,
	}, nil
}

func (pool *UserOpPool) ReSimulateUserOp(simmedUserOp *types.SimulatedUserOp, entryPointAddr *common.Address) error {
	simulator := pool.newSimulator()

	accessList, gasUsed, simErr := simulator.SimulateUserOp(simmedUserOp.UserOp, entryPointAddr)
	if simErr != nil {
		return simErr
	}

	if !accessList.Equals(simmedUserOp.AccessList) || gasUsed != simmedUserOp.GasUsed {
		return ErrUserOpReSimulationFailed
	}

	return nil
}

// bundleUserOperations checks the user operation pool and bundles
// them into a transaction to later be wrapped in an MevBundle
func (pool *UserOpPool) bundleByEntryPoint(head *types.Block, entryPointAddr *common.Address) error {
	gasLimit := head.GasLimit()
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
		if err := pool.ReSimulateUserOp(simmed, entryPointAddr); err != nil {
			log.Warn("Drop user operation because it didn't pass resimulation")
			delete(pool.bySender, simmed.Sender)
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
		// totalAccessList.Extend(simmed.AccessList) // TODO
		userOps = append(userOps, simmed.UserOp)
		pool.byEntryPoint[*entryPointAddr] = append(userOpList[:i], userOpList[i+1:]...)

		// TODO
		// Not sure if this is the correct fee for the tx
		// Maybe gasFee should be the sum of all bundled userOp gasFee
		gasFee := misc.CalcBaseFee(pool.txPool.chainconfig, head.Header())
		signedTx, err := pool.bundleIntoTx(userOps, entryPointAddr, gasFee)
		if err != nil {
			return err
		}

		log.Warn("Add bundled transaction to mev bundle pool")
		nextBlockNumber := new(big.Int).Add(head.Number(), common.Big1)
		if err := pool.txPool.AddMevBundle(types.Transactions{signedTx}, nextBlockNumber, 0, 0, nil); err != nil {
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
	entryPointAbi := entrypoint.NewEntryPointAbi()

	var (
		err  error
		data []byte
	)
	if len(userOps) == 1 {
		data, err = entryPointAbi.PackHandleOp(userOps[0], pool.signerAccount.Address)
	} else {
		data, err = entryPointAbi.PackHandleOps(userOps, pool.signerAccount.Address)
	}
	if err != nil {
		return nil, err
	}

	gas := uint64(0)
	for _, uo := range userOps {
		gas += uo.VerificationGas + uo.CallGas + overheadGas
	}

	chainId := pool.txPool.chainconfig.ChainID
	tx := types.NewTx(&types.DynamicFeeTx{
		To:        entryPointAddr,
		ChainID:   chainId,
		Gas:       gas,
		GasFeeCap: gasFee,
		GasTipCap: common.Big0,
		Value:     common.Big0,
		Data:      data,
	})

	return pool.signerWallet.SignTx(pool.signerAccount, tx, chainId)
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
		if err := pool.ReSimulateUserOp(simmed, entryPointAddr); err != nil {
			log.Warn("Dropped user operation because it didn't pass resimulation")
			delete(pool.bySender, simmed.Sender)
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
