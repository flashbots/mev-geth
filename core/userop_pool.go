package core

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/contracts/entrypoint"
	"github.com/ethereum/go-ethereum/core/state"
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
	SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription
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

type wallet interface {
	SignTx(accounts.Account, *types.Transaction, *big.Int) (*types.Transaction, error)
}

type simmedUserOp struct {
	types.UserOp
	al types.AccessList
}

type pooledUserOp struct {
	simmedUserOp
	invalid bool
}

type pooledUserOps []*pooledUserOp

type UserOpPoolConfig struct {
	MaxBundled   uint
	MaxPerSender uint
	EntryPoints  []common.Address
}

type UserOpPool struct {
	mu sync.RWMutex
	wg sync.WaitGroup

	config UserOpPoolConfig

	stopCh       chan struct{}
	logsCh       chan []*types.Log
	logsSub      event.Subscription
	chainHeadCh  chan ChainHeadEvent
	chainHeadSub event.Subscription

	chainConfig *params.ChainConfig
	chain       blockChainWithContext
	mevPool     mevPool
	noncer      noncer

	bundling      bool
	etherbase     common.Address
	signerWallet  wallet
	signerAccount accounts.Account

	head  *types.Block
	state *state.StateDB

	// sender -> (reqId -> userop)
	byReqId map[common.Hash]*pooledUserOp

	// sender -> (nonce -> userop)
	bySender map[common.Address]map[uint64]*pooledUserOp

	// entrypoint -> [userops]
	byEntryPoint map[common.Address]pooledUserOps
}

func NewUserOpPool(config UserOpPoolConfig, chainConfig *params.ChainConfig, chain blockChainWithContext, mevPool mevPool, noncer noncer) *UserOpPool {
	pool := &UserOpPool{
		config: config,

		stopCh:      make(chan struct{}),
		logsCh:      make(chan []*types.Log),
		chainHeadCh: make(chan ChainHeadEvent),

		chainConfig: chainConfig,
		chain:       chain,
		mevPool:     mevPool,
		noncer:      noncer,

		byReqId:      make(map[common.Hash]*pooledUserOp),
		bySender:     make(map[common.Address]map[uint64]*pooledUserOp),
		byEntryPoint: make(map[common.Address]pooledUserOps),
	}

	pool.logsSub = pool.chain.SubscribeLogsEvent(pool.logsCh)
	pool.chainHeadSub = pool.chain.SubscribeChainHeadEvent(pool.chainHeadCh)
	pool.setHead(pool.chain.CurrentBlock())

	return pool
}

// mainLoop is the userOp pool's main event loop, waiting for and reacting to
// outside blockchain events
func (pool *UserOpPool) mainLoop() {
	pool.wg.Add(1)
	defer pool.wg.Done()

	for {
		select {
		// TODO(jmederos): don't lock inside cases

		case logs := <-pool.logsCh:
			pool.mu.RLock()
			for _, log := range logs {
				if _, supported := pool.byEntryPoint[log.Address]; !supported {
					continue
				}

				// TODO(jmederos): remove included userop according to event/log
				//
				// event UserOperationEvent(
				//   bytes32 indexed requestId,
				//   address indexed sender,
				//   address indexed paymaster,
				//   uint nonce,
				//   uint actualGasCost,
				//   uint actualGasPrice,
				//   bool success
				// );
				if pooled, present := pool.byReqId[log.Topics[1]]; present {
					pooled.invalid = true
				}
			}
			pool.mu.RUnlock()

		case ev := <-pool.chainHeadCh:
			pool.setHead(ev.Block)

			pool.mu.RLock()
			for entryPointAddr, byEntryPoint := range pool.byEntryPoint {
				ops := make([]*pooledUserOp, len(byEntryPoint))
				copy(ops, byEntryPoint)
				sort.Slice(ops, func(i, j int) bool {
					return ops[i].GasTipCap.Cmp(ops[j].GasTipCap) < 0
				})
				go pool.bundleByEntryPoint(entryPointAddr, ops)
			}
			pool.mu.RUnlock()

		case <-pool.stopCh:
			return

		case <-pool.logsSub.Err():
			return

		case <-pool.chainHeadSub.Err():
			return
		}
	}
}

func (pool *UserOpPool) setHead(head *types.Block) error {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	var err error
	pool.head = head
	pool.state, err = pool.chain.StateAt(pool.head.Root())
	return err
}

func (pool *UserOpPool) Start() {
	go pool.mainLoop()
}

func (pool *UserOpPool) Stop() {
	// Unsubscribe subscriptions registered from blockchain
	pool.logsSub.Unsubscribe()
	pool.chainHeadSub.Unsubscribe()

	// tell mainLoop that we need to shutdown
	close(pool.stopCh)

	// Wait for workers to stop
	pool.wg.Wait()

	log.Info("UserOp pool stopped")
}

func (pool *UserOpPool) SetSigner(wallet wallet, account accounts.Account) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.signerWallet = wallet
	pool.signerAccount = account
}

func (pool *UserOpPool) SetEtherbase(etherbase common.Address) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.etherbase = etherbase
}

func (pool *UserOpPool) Bundling() bool {
	return pool.bundling
}

// AddUserOp adds a user operation to the pool
func (pool *UserOpPool) AddUserOp(userOp *types.UserOp) (common.Hash, error) {
	// TODO: Sanity checks here, check out nethermind

	pool.mu.Lock()
	defer pool.mu.Unlock()

	toUpgrade, err := pool.validate(userOp)
	if err != nil {
		return common.Hash{}, err
	}

	simmed, err := pool.simulateOnAdd(userOp)
	if err != nil {
		return common.Hash{}, err
	}

	requestId, err := simmed.RequestId(pool.chainConfig.ChainID)
	if err != nil {
		return requestId, err
	} else if toUpgrade != nil {
		// remove from the pool and bySender map the user operation that is being upgraded
		toUpgrade.invalid = true
		toUpgradeReqId, _ := toUpgrade.RequestId(pool.chainConfig.ChainID)
		log.Info("Upgrading user operation", "Old", toUpgradeReqId, "New", requestId)
	} else {
		log.Info("Adding user operation", "RequestId", requestId)
	}

	// create pooled user op
	pooled := pooledUserOp{simmed, false}

	// if bySender has never been used for this sender
	if pool.bySender[userOp.Sender] == nil {
		// initialize the `nonce -> op` map
		pool.bySender[userOp.Sender] = make(map[uint64]*pooledUserOp)
	}

	// add the new user operation to pool maps
	pool.byReqId[requestId] = &pooled
	if len(userOp.InitCode) == 0 {
		pool.bySender[userOp.Sender][userOp.Nonce.Uint64()] = &pooled
	}
	pool.byEntryPoint[userOp.EntryPoint] = append(pool.byEntryPoint[userOp.EntryPoint], &pooled)

	return requestId, nil
}

func (pool *UserOpPool) validate(op *types.UserOp) (*pooledUserOp, error) {
	if byNonce, ok := pool.bySender[op.Sender]; ok {
		if toUpgrade, isUpgrade := byNonce[op.Nonce.Uint64()]; isUpgrade {
			// this must be an upgrade attempt
			if op.GasTipCap.Cmp(toUpgrade.GasTipCap) <= 0 {
				// not enough increment of gas fee for upgrade
				return nil, ErrSenderNonceUserOpInPool
			}

			requiredInc := new(big.Int).Div(toUpgrade.GasTipCap, new(big.Int).SetUint64(10))
			if new(big.Int).Sub(op.GasTipCap, toUpgrade.GasTipCap).Cmp(requiredInc) <= 0 {
				// not enough increment of gas fee for upgrade
				return nil, ErrSenderNonceUserOpInPool
			}

			return toUpgrade, nil

		} else if uint(len(pool.bySender[op.Sender])) >= pool.config.MaxPerSender {
			return nil, ErrSenderNonceUserOpInPool
		}
	}
	return nil, nil
}

func (pool *UserOpPool) simulateUserOp(userOp *types.UserOp) (types.AccessList, error) {
	blockCtx := NewEVMBlockContext(pool.head.Header(), pool.chain, &common.Address{})
	txCtx := vm.TxContext{
		Origin:   common.Address{},
		GasPrice: pool.head.BaseFee(),
	}
	tracer := vm.NewUserOpTracer(userOp.EntryPoint, userOp.Paymaster, userOp.Sender)
	evm := vm.NewEVM(blockCtx, txCtx, pool.state, pool.chainConfig, vm.Config{
		Tracer: tracer,
		Debug:  true,
	})

	data, err := entrypoint.Contract.PackSimulateValidation(userOp)
	if err != nil {
		return nil, err
	}

	userOpGas := userOp.PreVerificationGas.Uint64() + userOp.VerificationGas.Uint64()
	res, _, vmErr := evm.Call(vm.AccountRef(common.Address{}), userOp.EntryPoint, data, userOpGas, common.Big0)
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

func (pool *UserOpPool) simulateOnAdd(op *types.UserOp) (simmedUserOp, error) {
	simmed := simmedUserOp{UserOp: *op}
	if al, err := pool.simulateUserOp(op); err == nil {
		simmed.al = al
	} else {
		return simmed, err
	}

	for _, pooled := range pool.bySender[op.Sender] {
		if alOverlaps(simmed.al, pooled.al) {
			return simmed, ErrUserOpSimulationFailed
		}
	}

	return simmed, nil
}

// bundleUserOperations checks the user operation pool and bundles
// them into a transaction to later be wrapped in an MevBundle
func (pool *UserOpPool) bundleByEntryPoint(entryPointAddr common.Address, byEntryPoint []*pooledUserOp) error {
	maxBundles := pool.config.MaxBundled

	includedUserOps := []*types.UserOp{}
	includedAccessLists := []types.AccessList{}

	bundlesCreated := uint(0)
BundleLoop:
	for _, pooled := range byEntryPoint {
		if pooled.invalid {
			continue
		}

		if _, err := pool.simulateUserOp(&pooled.UserOp); err != nil {
			log.Info("Drop user operation because it didn't pass resimulation")
			pooled.invalid = true
			continue
		}

		for _, al := range includedAccessLists {
			if alOverlaps(pooled.al, al) {
				continue BundleLoop
			}
		}

		includedUserOps = append(includedUserOps, &pooled.UserOp)
		includedAccessLists = append(includedAccessLists, pooled.al)

		signedTx, err := pool.bundleIntoTx(includedUserOps, entryPointAddr)
		if err != nil {
			return err
		}

		targetBlock := new(big.Int).Add(pool.head.Number(), common.Big1)
		if err := pool.mevPool.AddMevBundle(types.Transactions{signedTx}, targetBlock, 0, 0, nil); err != nil {
			return err
		}
		log.Info("Add bundled transaction to mev bundle pool", "len", len(includedUserOps), "hash", signedTx.Hash())

		bundlesCreated += 1
		if bundlesCreated >= maxBundles {
			break
		}
	}

	return nil
}

// bundleUserOperations checks the user operation pool and bundles
// them into a transaction to later be wrapped in an MevBundle
func (pool *UserOpPool) bundleIntoTx(userOps []*types.UserOp, entryPointAddr common.Address) (*types.Transaction, error) {
	var (
		err  error
		data []byte
	)
	if len(userOps) == 1 {
		data, err = entrypoint.Contract.PackHandleOp(userOps[0], pool.etherbase)
	} else {
		data, err = entrypoint.Contract.PackHandleOps(userOps, pool.etherbase)
	}
	if err != nil {
		return nil, err
	}

	// TODO: eth_estimateGas: gas
	// Investigate this a little more, there is no easy
	// way in geth to do this because method for this is on api
	gas := uint64(0)
	gasTip := big.NewInt(0)
	for _, uo := range userOps {
		gas += uo.PreVerificationGas.Uint64() + uo.VerificationGas.Uint64() + uo.CallGas.Uint64() + overheadGas
		gasTip = math.BigMax(gasTip, uo.GasTipCap)
	}
	baseFee := misc.CalcBaseFee(pool.chainConfig, pool.head.Header())
	gasFee := new(big.Int).Add(baseFee, gasTip)

	chainId := pool.chainConfig.ChainID
	nonce := pool.noncer.Nonce(pool.signerAccount.Address)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainId,
		Nonce:     nonce,
		To:        &entryPointAddr,
		Gas:       gas,
		GasFeeCap: gasFee,
		GasTipCap: gasTip,
		Value:     common.Big0,
		Data:      data,
	})

	return pool.signerWallet.SignTx(pool.signerAccount, tx, chainId)
}

func alOverlaps(al1 types.AccessList, al2 types.AccessList) bool {
	for _, tuple1 := range al1 {
		for _, tuple2 := range al2 {
			if tuple1.Address != tuple2.Address {
				continue
			}

			for _, slot1 := range tuple1.StorageKeys {
				for _, slot2 := range tuple2.StorageKeys {
					if slot1 == slot2 {
						return true
					}
				}
			}
		}
	}
	return false
}
