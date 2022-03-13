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
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/contracts/entrypoint"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

const (
	overheadGas        = uint64(0)
	banSlack           = 50
	throttlingSlack    = 10
	minInclBundling    = 10
	minInclNotBundling = 100
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
	ErrUserOpReSimulationFailed = errors.New("Simulation of user operation failed while being in the pool")
)

type blockChainWithLogSubAndContext interface {
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
	simmedAt *big.Int
	al       types.AccessList
}

type pooledUserOp struct {
	simmedUserOp
	invalid bool
}

type pooledUserOps []*pooledUserOp

type UserOpPoolConfig struct {
	MaxBundled   uint
	MaxPerSender uint

	EntryPoints []common.Address
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
	opFeed       event.Feed
	scope        event.SubscriptionScope

	chainConfig *params.ChainConfig
	chain       blockChainWithLogSubAndContext
	mevPool     mevPool
	noncer      noncer

	bundling      bool
	etherbase     common.Address
	signerWallet  wallet
	signerAccount accounts.Account

	head  *types.Block
	state *state.StateDB

	// requestId -> userop
	byReqId map[common.Hash]*pooledUserOp

	// sender -> (nonce -> userop)
	bySender map[common.Address]map[uint64]*pooledUserOp

	// paymaster -> [userops]
	byPaymaster map[common.Address]pooledUserOps

	// entrypoint -> [userops]
	byEntryPoint map[common.Address]pooledUserOps

	// addr -> paymaster throttler
	throttlers map[common.Address]*throttler
}

func NewUserOpPool(config UserOpPoolConfig, chainConfig *params.ChainConfig, chain blockChainWithLogSubAndContext, mevPool mevPool, noncer noncer) *UserOpPool {
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
		byPaymaster:  make(map[common.Address]pooledUserOps),
		byEntryPoint: make(map[common.Address]pooledUserOps),

		throttlers: make(map[common.Address]*throttler),
	}
	pool.setHead(pool.chain.CurrentBlock())

	pool.logsSub = pool.chain.SubscribeLogsEvent(pool.logsCh)
	pool.chainHeadSub = pool.chain.SubscribeChainHeadEvent(pool.chainHeadCh)

	pool.wg.Add(1)
	go pool.mainLoop()

	return pool
}

func (pool *UserOpPool) newThrottler(addr common.Address) *throttler {
	th := throttler{
		addr:            addr,
		banSlack:        banSlack,
		throttlingSlack: throttlingSlack,
	}
	if pool.bundling {
		th.minInclRateDenominator = minInclBundling
	} else {
		th.minInclRateDenominator = minInclNotBundling
	}
	return &th
}

// mainLoop is the userOp pool's main event loop, waiting for and reacting to
// outside blockchain events
func (pool *UserOpPool) mainLoop() {
	defer pool.wg.Done()

	throttlerTicker := time.NewTicker(1 * time.Hour)
	defer throttlerTicker.Stop()

	for {
		select {
		// TODO(jmederos): don't lock inside cases

		case <-throttlerTicker.C:
			pool.mu.RLock()
			for _, th := range pool.throttlers {
				th.amortize()
			}
			pool.mu.RUnlock()

		case logs := <-pool.logsCh:
			pool.mu.RLock()
			for _, l := range logs {
				entryPoint := l.Address
				if _, isSupported := pool.byEntryPoint[entryPoint]; !isSupported {
					continue
				}
				log.Debug("Processing new log from supported entryPoint", "entryPoint", entryPoint, "blockNumber", l.BlockNumber)

				// event UserOperationEvent(
				//   bytes32 indexed requestId,
				//   address indexed sender,
				//   address indexed paymaster,
				//   uint nonce,
				//   uint actualGasCost,
				//   uint actualGasPrice,
				//   bool success
				// );
				eventSig := "UserOperationEvent(bytes32,address,address,uint256,uint256,uint256,bool)"
				eventHash := crypto.Keccak256Hash([]byte(eventSig))
				if l.Topics[0] != eventHash {
					continue
				}

				reqid := l.Topics[1]
				log.Debug("Processing UserOperationEvent from log", "requestId", reqid.Hex())
				if pooled, present := pool.byReqId[reqid]; present {
					log.Debug("Removing userop from pool after inclusion in block", "reqid", reqid.Hex())
					pooled.invalid = true

					throttler := pool.throttlers[pooled.Paymaster]
					throttler.addIncl()
				}
			}
			pool.mu.RUnlock()

		case ev := <-pool.chainHeadCh:
			pool.setHead(ev.Block)
			if !pool.bundling {
				log.Debug("Dismissing head event as we're not bundling")
				continue
			}

			go func() {
				pool.mu.RLock()
				defer pool.mu.RUnlock()

				for pm, ops := range pool.byPaymaster {
					if th, known := pool.throttlers[pm]; known && th.throttled() {
						for _, op := range ops {
							if new(big.Int).Sub(pool.head.Number(), op.simmedAt).Uint64() > 10 {
								op.invalid = true
							}
						}
					}
				}
			}()

			pool.mu.RLock()
			for entryPointAddr, byEntryPoint := range pool.byEntryPoint {
				// TODO(jmederos): maybe wrap this in `go func() {...`
				ops := make([]*pooledUserOp, len(byEntryPoint))
				copy(ops, byEntryPoint)
				sort.Slice(ops, func(i, j int) bool {
					return ops[i].GasTipCap.Cmp(ops[j].GasTipCap) < 0
				})
				go pool.bundleByEntryPoint(entryPointAddr, ops)
			}
			pool.mu.RUnlock()

		case <-pool.chainHeadSub.Err():
			return

		case <-pool.stopCh:
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
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.bundling = true
	for _, th := range pool.throttlers {
		th.mu.Lock()
		th.minInclRateDenominator = minInclBundling
		th.mu.Unlock()
	}

	log.Info("Enable bundling of user operations", "signer", pool.signerAccount.Address.Hex())
}

func (pool *UserOpPool) Stop() {
	// Unsubscribe all subscriptions registered from oppool
	pool.scope.Close()

	// Unsubscribe subscriptions registered from blockchain
	pool.chainHeadSub.Unsubscribe()

	// tell mainLoop that we need to shutdown
	close(pool.stopCh)

	// Wait for workers to stop
	pool.wg.Wait()

	log.Info("UserOp pool stopped")
}

func (pool *UserOpPool) SubscribeNewUserOpsEvent(ch chan<- NewUserOpsEvent) event.Subscription {
	return pool.scope.Track(pool.opFeed.Subscribe(ch))
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

func (pool *UserOpPool) Get(reqId common.Hash) *types.UserOp {
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	return &pool.byReqId[reqId].UserOp
}

// AddUserOp adds a user operation to the pool
func (pool *UserOpPool) AddUserOp(userOp *types.UserOp) (common.Hash, error) {
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

	requestId, err := simmed.RequestId()
	if err != nil {
		log.Error("Error computing request id", "err", err)
		return requestId, err
	} else if toUpgrade != nil {
		// remove from the pool and bySender map the user operation that is being upgraded
		toUpgrade.invalid = true
		toUpgradeReqId, _ := toUpgrade.RequestId()
		log.Info("Upgrading user operation", "old", toUpgradeReqId.Hex(), "new", requestId.Hex())
	} else {
		log.Info("Adding user operation", "requestId", requestId.Hex())
	}

	// create pooled user op
	pooled := pooledUserOp{simmed, false}

	// TODO(jmederos): move this to mainLoop
	// handle this with channels and queues
	//
	// add the new user operation to pool maps
	pool.byReqId[requestId] = &pooled
	pool.byEntryPoint[userOp.EntryPoint] = append(pool.byEntryPoint[userOp.EntryPoint], &pooled)
	if len(userOp.InitCode) == 0 {
		// if bySender has never been used for this sender
		if _, known := pool.bySender[userOp.Sender]; !known {
			// initialize the `nonce -> op` map
			pool.bySender[userOp.Sender] = make(map[uint64]*pooledUserOp)
		}
		pool.bySender[userOp.Sender][userOp.Nonce.Uint64()] = &pooled
	}
	if userOp.Paymaster != (common.Address{}) {
		pool.byPaymaster[userOp.Paymaster] = append(pool.byPaymaster[userOp.Paymaster], &pooled)
		// if paymasters has never been used for this paymaster
		if _, known := pool.throttlers[userOp.Paymaster]; !known {
			// initialize the `paymaster -> status` map
			pool.throttlers[userOp.Paymaster] = pool.newThrottler(userOp.Paymaster)
		}
		throttler := pool.throttlers[userOp.Paymaster]
		throttler.addSeen()
	}
	pool.opFeed.Send(NewUserOpsEvent{[]*types.UserOp{userOp}})

	return requestId, nil
}

func (pool *UserOpPool) validate(op *types.UserOp) (*pooledUserOp, error) {
	if throttler, ok := pool.throttlers[op.Paymaster]; ok {
		if throttler.banned() {
			return nil, fmt.Errorf("Paymaster banned")
		} else if throttler.throttled() {
			for _, op := range pool.byPaymaster[op.Paymaster] {
				if !op.invalid {
					return nil, fmt.Errorf("Paymaster throttled and already has ops in the pool")
				}
			}
		}
	}

	if byNonce, ok := pool.bySender[op.Sender]; ok {
		if toUpgrade, isUpgrade := byNonce[op.Nonce.Uint64()]; isUpgrade && !toUpgrade.invalid {
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
	log.Debug("Simulating user operation", "blockNumber", pool.head.Number())

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
	simmed := simmedUserOp{UserOp: *op, simmedAt: pool.head.Number()}
	if al, err := pool.simulateUserOp(op); err == nil {
		simmed.al = al
	} else {
		return simmed, err
	}

	for _, pooled := range pool.bySender[op.Sender] {
		if !pooled.invalid && alOverlaps(simmed.al, pooled.al) {
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
		log.Info("Add bundled transaction to mev bundle pool", "len", len(includedUserOps), "hash", signedTx.Hash().Hex())

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

type throttler struct {
	mu sync.RWMutex

	addr common.Address

	minInclRateDenominator    uint
	throttlingSlack, banSlack uint

	seen uint
	incl uint
}

func (th *throttler) amortize() {
	th.mu.Lock()
	defer th.mu.Unlock()
	th.seen -= th.seen / 24
	th.incl -= th.incl / 24
}

func (th *throttler) addSeen() {
	th.mu.Lock()
	defer th.mu.Unlock()
	th.seen += 1
}

func (th *throttler) addIncl() {
	th.mu.Lock()
	defer th.mu.Unlock()
	th.incl += 1
}

func (th *throttler) banned() bool {
	th.mu.RLock()
	defer th.mu.RUnlock()
	return th.seen/th.minInclRateDenominator > th.incl+th.banSlack
}

func (th *throttler) throttled() bool {
	th.mu.RLock()
	defer th.mu.RUnlock()
	return th.seen/th.minInclRateDenominator > th.incl+th.throttlingSlack && !th.banned()
}
