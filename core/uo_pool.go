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

// TODO: Check error messages
var (
	// ErrSignerAccountNotFound is returned if the provided signer
	// doesn't have an account registered under AccountManager
	ErrSignerAccountNotFound = errors.New("The provided signer address doesn't have an account associated or we're not managing any account")

	// ErrSenderUserOperationInPool is returned if the pool already contains a
	// user operation with the given sender and nonce, but the gas tip given wasn't enough to upgrade it.
	ErrSenderNonceUserOpInPool = errors.New("Sender already has a user operation in the pool with this nonce and GasTip is not enough to upgrade")

	// ErrSenderAllowanceExceeded is returned if the pool already contains
	// the maximum allowed amount of user operations for the given sender.
	ErrSenderAllowanceExceeded = errors.New("Sender already reached the maximum allowed amount of user operations in the pool")

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

type noncer interface {
	Nonce(addr common.Address) uint64
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

type uoPoolLocation struct {
	entryPointAddr common.Address
	index          int
}

type UserOpPoolConfig struct {
	signerAddr          common.Address
	maxBundles          uint
	maxUserOpsPerSender uint
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
	noncer      noncer
	signer      signer

	// tx hash -> [index]
	// `index`: is the index of the op in the corresponding `byEntryPoint` pool
	byTxHash map[common.Hash][]uoPoolLocation

	// sender -> (nonce -> location)
	// `index`: is the index of the op in the corresponding `byEntryPoint` pool
	bySender map[common.Address]map[uint64]uoPoolLocation

	// entrypoint -> [user operations]
	byEntryPoint map[common.Address][]*types.SimulatedUserOp
}

func NewUserOpPool(config UserOpPoolConfig, chainConfig *params.ChainConfig, chain blockChainWithContext, mevPool mevPool, noncer noncer, accountManager *accounts.Manager) (*UserOpPool, error) {
	// TODO: Find a better place for this {{{

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

	// TODO: Figure out how to properly set default config values
	if config.maxBundles == 0 {
		config.maxBundles = 1
	}

	// TODO: Figure out how to properly set default config values
	if config.maxUserOpsPerSender == 0 {
		config.maxUserOpsPerSender = 1
	}

	// }}}

	pool := &UserOpPool{
		config: config,

		initDoneCh:  make(chan struct{}),
		chainHeadCh: make(chan ChainHeadEvent),

		chainConfig: chainConfig,
		chain:       chain,
		mevPool:     mevPool,
		noncer:      noncer,
		signer:      &walletSigner{wallet, account, chainConfig.ChainID},

		byTxHash:     make(map[common.Hash][]uoPoolLocation),
		bySender:     make(map[common.Address]map[uint64]uoPoolLocation),
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

				for _, tx := range ev.Block.Transactions() {
					if locations, ok := pool.byTxHash[tx.Hash()]; ok {
						for _, loc := range locations {
							pool.removeUserOp(loc)
						}
					}
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
	if pool.bundleTicker != nil {
		pool.bundleTicker.Stop()
	}

	// Wait for workers to stop
	pool.wg.Wait()

	log.Info("UserOp pool stopped")
}

// AddUserOp adds a user operation to the pool
func (pool *UserOpPool) AddUserOp(userOp *types.UserOp, entryPointAddr *common.Address) error {
	// TODO: Sanity checks here, check out nethermind

	pool.mu.Lock()
	defer pool.mu.Unlock()

	var (
		toUpgrade uoPoolLocation
		isUpgrade bool
	)
	if byNonce, ok := pool.bySender[userOp.Sender]; ok {
		if toUpgrade, isUpgrade = byNonce[userOp.Nonce]; isUpgrade {
			// this must be an upgrade attempt
			// get op that the user is trying to upgrade
			poolUserOp := pool.byEntryPoint[toUpgrade.entryPointAddr][toUpgrade.index]
			if userOp.GasTipCap.Cmp(poolUserOp.GasTipCap) <= 0 {
				// not enough increment of gas fee for upgrade
				return ErrSenderNonceUserOpInPool
			}
			requiredInc := new(big.Int).Div(poolUserOp.GasTipCap, new(big.Int).SetUint64(10))
			if new(big.Int).Sub(userOp.GasTipCap, poolUserOp.GasTipCap).Cmp(requiredInc) <= 0 {
				// not enough increment of gas fee for upgrade
				return ErrSenderNonceUserOpInPool
			}
		}
	}

	if !isUpgrade && uint(len(pool.bySender[userOp.Sender])) >= pool.config.maxUserOpsPerSender {
		return ErrSenderNonceUserOpInPool
	}

	simmed, err := pool.simulateUserOp(userOp, entryPointAddr)
	if err != nil {
		log.Warn("Discarding user operation because it didn't pass simulation")
		return err
	}

	log.Warn("Adding user operation to the pool")
	if isUpgrade {
		// remove from the pool and bySender map the user operation that is being upgraded
		pool.removeUserOp(toUpgrade)
	}

	// if bySender has never been used for this sender
	if pool.bySender[userOp.Sender] == nil {
		// initialize the `nonce -> op` map
		pool.bySender[userOp.Sender] = make(map[uint64]uoPoolLocation)
	}
	// add the new user operation to the pool and bySender map
	pool.bySender[userOp.Sender][userOp.Nonce] = uoPoolLocation{
		entryPointAddr: *entryPointAddr,
		index:          len(pool.byEntryPoint[*entryPointAddr]),
	}
	pool.byEntryPoint[*entryPointAddr] = append(pool.byEntryPoint[*entryPointAddr], simmed)
	return nil
}

func (pool *UserOpPool) removeUserOp(location uoPoolLocation) {
	byEntryPoint := pool.byEntryPoint[location.entryPointAddr]
	userOp := byEntryPoint[location.index]
	pool.byEntryPoint[location.entryPointAddr] = append(byEntryPoint[:location.index], byEntryPoint[location.index+1:]...)
	delete(pool.bySender[userOp.Sender], userOp.Nonce)
}

func (pool *UserOpPool) SimulateUserOp(userOp *types.UserOp, entryPointAddr *common.Address, block *types.Block) (types.AccessList, error) {
	state, err := pool.chain.StateAt(block.Root())
	if err != nil {
		return nil, err
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
		return nil, err
	}

	userOpGas := userOp.PreVerificationGas + userOp.VerificationGas
	res, _, vmErr := evm.Call(vm.AccountRef(common.Address{}), *entryPointAddr, data, userOpGas, common.Big0)
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
		return nil, vmErr
	}

	if tracerErr := tracer.Err(); tracerErr != nil {
		return nil, tracerErr
	}

	return tracer.AccessList(), nil
}

func (pool *UserOpPool) simulateUserOp(userOp *types.UserOp, entryPointAddr *common.Address) (*types.SimulatedUserOp, error) {
	block := pool.chain.CurrentBlock()
	accessList, simErr := pool.SimulateUserOp(userOp, entryPointAddr, block)
	if simErr != nil {
		return nil, simErr
	}

	simmed := &types.SimulatedUserOp{
		UserOp:     userOp,
		AccessList: accessList,
	}

	for _, loc := range pool.bySender[userOp.Sender] {
		poolUserOp := pool.byEntryPoint[loc.entryPointAddr][loc.index]
		if accessList.Overlaps(poolUserOp.AccessList) {
			// TODO: Maybe this error wrapping is overly complicated
			return simmed, fmt.Errorf("%w (%s)", ErrUserOpSimulationFailed, "User operation access list overlaps with those already in the pool")
		}
	}

	return simmed, nil
}

func (pool *UserOpPool) reSimulateUserOp(simmedUserOp *types.SimulatedUserOp, entryPointAddr *common.Address) error {
	block := pool.chain.CurrentBlock()
	accessList, simErr := pool.SimulateUserOp(simmedUserOp.UserOp, entryPointAddr, block)
	if simErr != nil {
		return simErr
	}

	if !accessList.Equals(simmedUserOp.AccessList) {
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
		return userOpList[i].GasTipCap.Cmp(userOpList[j].GasTipCap) < 0
	})

	includedUserOps := []*types.UserOp{}
	includedLocations := []uoPoolLocation{}
	includedAccessLists := []types.AccessList{}
	totalGasUsed := uint64(0)

	bundlesCreated := uint(0)
BundleLoop: // TODO: Remove this, I don't really like how this label looks here
	for _, simmed := range userOpList {
		simmedLocation := pool.bySender[simmed.Sender][simmed.Nonce] // it is used several times

		if totalGasUsed+simmed.PreVerificationGas+simmed.VerificationGas+simmed.CallGas > gasLimit {
			// Including this userOp in our transaction would make it exceed the block gasLimit
			continue
		}

		if err := pool.reSimulateUserOp(simmed, entryPointAddr); err != nil {
			log.Warn("Drop user operation because it didn't pass resimulation")
			pool.removeUserOp(simmedLocation)
			continue
		}

		for _, al := range includedAccessLists {
			if simmed.AccessList.Overlaps(al) {
				continue BundleLoop
			}
		}

		// TODO
		// Not sure if this is the correct fee for the tx
		// Maybe gasFee should be the sum of all bundled userOp gasFee
		gasFee := misc.CalcBaseFee(pool.chainConfig, head.Header())

		if simmed.GasFeeCap.Cmp(gasFee) < 0 {
			log.Warn("Drop user operation because gas fee cap bellow current base fee")
			pool.removeUserOp(pool.bySender[simmed.Sender][simmed.Nonce])
			continue
		}

		totalGasUsed += simmed.PreVerificationGas + simmed.VerificationGas + simmed.CallGas
		includedAccessLists = append(includedAccessLists, simmed.AccessList)
		includedUserOps = append(includedUserOps, simmed.UserOp)
		includedLocations = append(includedLocations, simmedLocation)

		signedTx, err := pool.bundleIntoTx(includedUserOps, entryPointAddr, gasFee)
		if err != nil {
			return err
		}

		log.Warn("Add bundled transaction to mev bundle pool")
		nextBlockNumber := new(big.Int).Add(head.Number(), common.Big1)
		if err := pool.mevPool.AddMevBundle(types.Transactions{signedTx}, nextBlockNumber, 0, 0, nil); err != nil {
			return err
		}

		pool.byTxHash[signedTx.Hash()] = append(pool.byTxHash[signedTx.Hash()], includedLocations...)

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
	// Investigate this a little more, there is no easy
	// way in geth to do this because method for this is on api
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
		gas += uo.PreVerificationGas + uo.VerificationGas + uo.CallGas + overheadGas
	}

	chainId := pool.chainConfig.ChainID
	nonce := pool.noncer.Nonce(pool.signer.Address())
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainId,
		Nonce:     nonce,
		To:        entryPointAddr,
		Gas:       gas,
		GasFeeCap: gasFee,
		GasTipCap: common.Big0,
		Value:     common.Big0,
		Data:      data,
	})

	return pool.signer.SignTx(tx)
}
