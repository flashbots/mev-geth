package vm

import (
	"fmt"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

func newBlockCtx() BlockContext {
	return BlockContext{
		CanTransfer: func(db StateDB, addr common.Address, amount *big.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db StateDB, sender, recipient common.Address, amount *big.Int) {
			db.SubBalance(sender, amount)
			db.AddBalance(recipient, amount)
		},
		GetHash: func(n uint64) common.Hash {
			return crypto.Keccak256Hash([]byte(new(big.Int).SetUint64(n).String()))
		},
		BlockNumber: new(big.Int),
		Time:        big.NewInt(time.Now().Unix()),
		Difficulty:  new(big.Int),
		GasLimit:    math.MaxUint64,
		BaseFee:     new(big.Int),
	}
}

func newTxCtx() TxContext {
	return TxContext{
		Origin:   common.Address{},
		GasPrice: new(big.Int),
	}
}

func newStateDB() StateDB {
	statedb, _ := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	return statedb
}

func newChainConfig() *params.ChainConfig {
	return &params.ChainConfig{
		ChainID:             big.NewInt(1),
		HomesteadBlock:      new(big.Int),
		DAOForkBlock:        new(big.Int),
		DAOForkSupport:      false,
		EIP150Block:         new(big.Int),
		EIP150Hash:          common.Hash{},
		EIP155Block:         new(big.Int),
		EIP158Block:         new(big.Int),
		ByzantiumBlock:      new(big.Int),
		ConstantinopleBlock: new(big.Int),
		PetersburgBlock:     new(big.Int),
		IstanbulBlock:       new(big.Int),
		MuirGlacierBlock:    new(big.Int),
		BerlinBlock:         new(big.Int),
		LondonBlock:         new(big.Int),
	}
}

func newTracedEVM(statedb StateDB, chainConfig *params.ChainConfig) (*UserOpTracer, *EVM) {
	blockCtx := newBlockCtx()
	txCtx := newTxCtx()
	tracer := NewUserOpTracer(common.Address{}, common.Address{}, common.Address{})
	config := Config{
		Tracer:    tracer,
		Debug:     true,
		NoBaseFee: true,
	}
	return tracer, NewEVM(blockCtx, txCtx, statedb, chainConfig, config)
}

// TODO: This is failing when when `selfbalance > 2` because we changed the
// paymaster validation mode marker from BALANCE opcode to NUMBER
func TestUserOpTracerBansSelfDestructAndDelegateCallOnlyWhenPaymasterModeIsActive(t *testing.T) {
	args := []struct {
		opCode        OpCode
		selfBalance   uint
		shouldSucceed bool
	}{
		{SELFDESTRUCT, 1, true},
		{DELEGATECALL, 1, true},
		{ADD, 1, true},
		{SELFDESTRUCT, 2, false},
		{DELEGATECALL, 2, false},
		{ADD, 2, true},
	}

	for _, arg := range args {
		t.Run(fmt.Sprintf("{OpCode=%v,SelfBalance=%v}", arg.opCode, arg.selfBalance), func(t *testing.T) {
			statedb := newStateDB()
			chainConfig := newChainConfig()
			tracer, evm := newTracedEVM(statedb, chainConfig)

			callee := common.BytesToAddress([]byte("callee"))
			statedb.CreateAccount(callee)
			caller := common.BytesToAddress([]byte("caller"))
			statedb.CreateAccount(caller)

			calleeCode := []byte{
				byte(PUSH1),
				0x01,
				byte(PUSH1),
				0x02,
				byte(PUSH1),
				0x03,
				byte(PUSH1),
				0x04,
				byte(PUSH1),
				0x05,
				byte(PUSH1),
				0x69,
				byte(arg.opCode),
			}

			selfBalanceOp := DUP1
			if arg.selfBalance > 1 {
				selfBalanceOp = SELFBALANCE
			}

			callerCode := []byte{byte(SELFBALANCE), byte(selfBalanceOp)}
			for i := 0; i < 5; i++ {
				callerCode = append(callerCode, byte(PUSH32))
				callerCode = append(callerCode, common.Hex2BytesFixed("0x00", 32)...)
			}
			callerCode = append(callerCode, byte(PUSH32))
			callerCode = append(callerCode, common.LeftPadBytes(callee.Bytes(), 32)...)
			callerCode = append(callerCode, byte(PUSH32))
			callerCode = append(callerCode, common.LeftPadBytes(new(big.Int).SetUint64(50000).Bytes(), 32)...)
			callerCode = append(callerCode, byte(CALL))
			callerCode = append(callerCode, byte(STOP))

			statedb.SetCode(callee, calleeCode)
			statedb.SetCode(caller, callerCode)

			_, _, err := evm.Call(AccountRef(evm.TxContext.Origin), caller, []byte{}, 100000, big.NewInt(0))
			tracerErr := tracer.Err()

			if arg.shouldSucceed {
				if err != nil {
					t.Fatalf("Call should succeed but excecution reverted with (err: %v)", err)
				}
				if tracerErr != nil {
					t.Fatalf("Call should succeed but tracer reported error (err: %v)", tracerErr)
				}
			} else if err == nil && tracerErr == nil {
				t.Fatalf("Call should fail but neither excecution reverted nor the tracer reported an error (tracer: {paymasterValidationMode: %v})", tracer.paymasterValidationMode)
			}
		})
	}
}

func TestUserOpTracerFailsIfBannedOpCodeIsUsedWhenCallDepthIsMoreThanOne(t *testing.T) {
	args := []struct {
		opCode        OpCode
		shouldSucceed bool
	}{
		{GASPRICE, false},
		{GASLIMIT, false},
		{DIFFICULTY, false},
		{TIMESTAMP, false},
		{BASEFEE, false},
		{BLOCKHASH, false},
		{NUMBER, false},
		{BALANCE, false},
		{SELFBALANCE, false},
		{ORIGIN, false},
		{BALANCE, false},
		{DUP1, true},
		{ISZERO, true},
		{AND, true},
	}

	for _, arg := range args {
		t.Run(fmt.Sprintf("{OpCode=%v}", arg.opCode), func(t *testing.T) {
			statedb := newStateDB()
			chainConfig := newChainConfig()
			tracer, evm := newTracedEVM(statedb, chainConfig)

			callee := common.BytesToAddress([]byte("callee"))
			statedb.CreateAccount(callee)
			caller := common.BytesToAddress([]byte("caller"))
			statedb.CreateAccount(caller)

			calleeCode := []byte{
				byte(PUSH1),
				0x01,
				byte(PUSH1),
				0x69,
				byte(arg.opCode),
			}

			callerCode := []byte{}
			for i := 0; i < 5; i++ {
				callerCode = append(callerCode, byte(PUSH32))
				callerCode = append(callerCode, common.Hex2BytesFixed("0x00", 32)...)
			}
			callerCode = append(callerCode, byte(PUSH32))
			callerCode = append(callerCode, common.LeftPadBytes(callee.Bytes(), 32)...)
			callerCode = append(callerCode, byte(PUSH32))
			callerCode = append(callerCode, common.LeftPadBytes(new(big.Int).SetUint64(50000).Bytes(), 32)...)
			callerCode = append(callerCode, byte(CALL))
			callerCode = append(callerCode, byte(STOP))

			statedb.SetCode(callee, calleeCode)
			statedb.SetCode(caller, callerCode)

			_, _, err := evm.Call(AccountRef(evm.TxContext.Origin), caller, []byte{}, 100000, big.NewInt(0))
			tracerErr := tracer.Err()

			if arg.shouldSucceed {
				if err != nil {
					t.Fatalf("Call should succeed but excecution reverted with (err: %v)", err)
				}
				if tracerErr != nil {
					t.Fatalf("Call should succeed but tracer reported error (err: %v)", tracerErr)
				}
			} else if err == nil && tracerErr == nil {
				t.Fatalf("Call should fail but neither excecution reverted nor the tracer reported an error (tracer: {paymasterValidationMode: %v})", tracer.paymasterValidationMode)
			}
		})
	}
}

func TestUserOpTracerSucceedsIfBannedOpCodeIsUsedWhenCallDepthIsOne(t *testing.T) {
	args := []struct {
		opCode        OpCode
		shouldSucceed bool
	}{
		{GASPRICE, true},
		{GASLIMIT, true},
		{DIFFICULTY, true},
		{TIMESTAMP, true},
		{BASEFEE, true},
		{BLOCKHASH, true},
		{NUMBER, true},
		{BALANCE, true},
		{SELFBALANCE, true},
		{ORIGIN, true},
		{BALANCE, true},
	}

	for _, arg := range args {
		t.Run(fmt.Sprintf("{OpCode=%v}", arg.opCode), func(t *testing.T) {
			statedb := newStateDB()
			chainConfig := newChainConfig()
			tracer, evm := newTracedEVM(statedb, chainConfig)

			callee := common.BytesToAddress([]byte("callee"))
			statedb.CreateAccount(callee)

			calleeCode := []byte{
				byte(PUSH1),
				0x01,
				byte(PUSH1),
				0x69,
				byte(arg.opCode),
			}

			statedb.SetCode(callee, calleeCode)

			_, _, err := evm.Call(AccountRef(evm.TxContext.Origin), callee, []byte{}, 100000, big.NewInt(0))
			tracerErr := tracer.Err()

			if arg.shouldSucceed {
				if err != nil {
					t.Fatalf("Call should succeed but excecution reverted with (err: %v)", err)
				}
				if tracerErr != nil {
					t.Fatalf("Call should succeed but tracer reported error (err: %v)", tracerErr)
				}
			} else if err == nil && tracerErr == nil {
				t.Fatalf("Call should fail but neither excecution reverted nor the tracer reported an error (tracer: {paymasterValidationMode: %v})", tracer.paymasterValidationMode)
			}
		})
	}
}
