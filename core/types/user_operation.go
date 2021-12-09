package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/contracts/entrypoint"
)

var (
	zeroAddr = common.HexToAddress("0x0000000000000000000000000000000000000000")
)

type UserOperation struct {
	Nonce     uint64
	Signature []byte

	PreVerificationGas uint64
	VerificationGas    uint64
	CallGas            uint64
	GasTipCap          *big.Int
	GasFeeCap          *big.Int

	InitCode      []byte
	Sender        *common.Address
	CallData      []byte
	Paymaster     *common.Address
	PaymasterData []byte
}

type SimulatedUserOperation struct {
	*UserOperation
	AccessList AccessList
	GasUsed    uint64
}

func (uo *UserOperation) ToSimulationMessage(baseFee *big.Int) (Message, error) {
	gasPrice := math.BigMin(new(big.Int).Add(uo.GasTipCap, baseFee), uo.GasFeeCap)
	entrypointContract, err := entrypoint.NewEntryPointContract()
	if err != nil {
		return Message{}, err
	}

	addr := entrypointContract.Address()
	data, err := entrypointContract.ABI().Pack("simulateValidation", uo)
	if err != nil {
		return Message{}, err
	}

	return NewMessage(
		zeroAddr, &addr, 0, new(big.Int),
		uo.VerificationGas, gasPrice, uo.GasFeeCap, uo.GasTipCap,
		data, nil, true,
	), nil
}
