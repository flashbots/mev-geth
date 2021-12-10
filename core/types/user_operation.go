package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
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

	AccessList AccessList
	GasUsed    uint64
}
