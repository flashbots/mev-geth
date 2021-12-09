package vm

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type UserOperationTracer struct {
	accessList   accessList
	forbiddenOps map[OpCode]uint

	mutableStateAccess bool
	mutableCall        bool
	forbiddenOp        bool

	entryPointSelfBalanceCount int // SELFBALANCE is called by the entrypoint twice before it starts validating paymasters
	paymasterValidationMode bool // only check for SELFDESTRUCT or DELEGATECALL when we are checking the paymasters

	depth   *int
	statedb *StateDB
}

func NewUserOperationTracer() *UserOperationTracer {
	return &UserOperationTracer{
		accessList: newAccessList(),
		forbiddenOps: map[OpCode]uint{
			GASPRICE: 0, GASLIMIT: 0, DIFFICULTY: 0, TIMESTAMP: 0,
			BASEFEE: 0, BLOCKHASH: 0, NUMBER: 0, SELFBALANCE: 0,
			BALANCE: 0, ORIGIN: 0, COINBASE: 0,
		},
		entryPointSelfBalanceCount: 0,
	}
}

func (uot *UserOperationTracer) AccessList() types.AccessList {
	return uot.accessList.accessList()
}

func (uot *UserOperationTracer) ForbiddenOp() bool {
	return uot.forbiddenOp
}

func (uot *UserOperationTracer) MutableCall() bool {
	return uot.mutableCall
}

func (uot *UserOperationTracer) MutableStateAccess() bool {
	return uot.mutableStateAccess
}

// CaptureStart implements the Tracer interface to initialize the tracing operation.
func (uot *UserOperationTracer) CaptureStart(env *EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	uot.depth = &env.depth
	uot.statedb = &env.StateDB
}

// CaptureEnd is called after the call finishes finalizing the tracing.
func (uot *UserOperationTracer) CaptureEnd(output []byte, gasUsed uint64, t time.Duration, err error) {
}

// CaptureState implements the Tracer interface to trace a single step of VM execution.
func (uot *UserOperationTracer) CaptureState(env *EVM, pc uint64, op OpCode, gas, cost uint64, scope *ScopeContext, rData []byte, depth int, err error) {
	if depth == 1 && op == SELFBALANCE {
		uot.entryPointSelfBalanceCount++
		if uot.entryPointSelfBalanceCount == 2 {
			uot.paymasterValidationMode = true
		}
	}

	if depth < 2 {
		return
	}

	stack := scope.Stack
	if (op == SLOAD || op == SSTORE) && stack.len() >= 1 {
		slot := common.Hash(stack.data[stack.len()-1].Bytes32())
		uot.accessList.addSlot(scope.Contract.Address(), slot)
	}

	if _, ok := uot.forbiddenOps[op]; ok {
		uot.forbiddenOps[op]++
		uot.forbiddenOp = true
	}
}

// CaptureFault implements the Tracer interface to trace an execution fault
func (uot *UserOperationTracer) CaptureFault(env *EVM, pc uint64, op OpCode, gas, cost uint64, scope *ScopeContext, depth int, err error) {
}

// CaptureEnter is called when EVM enters a new scope (via call, create or selfdestruct).
func (uot *UserOperationTracer) CaptureEnter(op OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	depth := *uot.depth
	if depth >= 2 {
		// check for mutable code (selfdestruct or delegatecall) in contract being called ONLY IF we are validating the paymaster
		if uot.paymasterValidationMode {
			if containsSelfDestructOrDelegateCall(uot.statedb, to) {
				uot.mutableCall = true
			}
		}
	}
}

// CaptureExit is called when EVM exits a scope, even if the scope didn't execute any code.
func (uot *UserOperationTracer) CaptureExit(output []byte, gasUsed uint64, err error) {
}

func containsSelfDestructOrDelegateCall(db *StateDB, address common.Address) bool {
	code := (*db).GetCode(address)
	for i := 0; i < len(code); i++ {
		currentOp := OpCode(code[i])
		if currentOp == SELFDESTRUCT || currentOp == DELEGATECALL {
			return true
		}

		if currentOp.IsPush() {
			i += int(currentOp) - int(PUSH1) + 1
		}
	}
	return false
}