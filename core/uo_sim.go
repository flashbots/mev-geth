package core

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/contracts/entrypoint"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

type UserOpSimulator struct {
	BlockNumber     *big.Int
	BlockDifficulty *big.Int // TODO: probably not needed
	GasLimit        uint64
	BaseFee         *big.Int
	GasPrice        *big.Int
	StateDB         vm.StateDB
	ChainConfig     *params.ChainConfig
}

func (simulator *UserOpSimulator) SimulateUserOp(userOp *types.UserOp, entryPointAddr *common.Address) (types.AccessList, uint64, error) {
	blockCtx := vm.BlockContext{
		Transfer:    Transfer,
		CanTransfer: CanTransfer,
		GetHash: func(n uint64) common.Hash {
			return crypto.Keccak256Hash([]byte("BLOCKHASH cannot effect simulation!"))
		},
		BlockNumber: simulator.BlockNumber,
		Difficulty:  simulator.BlockDifficulty,
		GasLimit:    simulator.GasLimit,
		BaseFee:     simulator.BaseFee,
		Time:        big.NewInt(time.Now().Unix()),
	}
	txCtx := vm.TxContext{
		Origin:   common.BytesToAddress([]byte("Miner address here")),
		GasPrice: simulator.BaseFee,
	}
	tracer := vm.NewUserOperationTracer()
	evm := vm.NewEVM(blockCtx, txCtx, simulator.StateDB, simulator.ChainConfig, vm.Config{
		Tracer: tracer,
		Debug:  true,
	})

	entrypoint := entrypoint.NewEntryPointAbi()
	data, err := entrypoint.PackSimulateValidation(userOp)
	if err != nil {
		log.Error("error packing")
		return nil, 0, err
	}

	_, gasLeft, vmErr := evm.Call(
		vm.AccountRef(evm.TxContext.Origin),
		*entryPointAddr,
		data,
		userOp.VerificationGas,
		common.Big0,
	)
	gasUsed := userOp.VerificationGas - gasLeft

	if vmErr != nil {
		return nil, gasUsed, vmErr
	}

	if tracerErr := tracer.Err(); tracerErr != nil {
		return nil, gasUsed, tracerErr
	}

	return tracer.AccessList(), gasUsed, nil
}
