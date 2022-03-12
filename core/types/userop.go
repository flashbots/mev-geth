package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type UserOp struct {
	Sender             common.Address
	Nonce              *big.Int
	InitCode           []byte
	CallData           []byte
	CallGas            *big.Int
	VerificationGas    *big.Int
	PreVerificationGas *big.Int
	GasFeeCap          *big.Int `abi:"maxFeePerGas"`
	GasTipCap          *big.Int `abi:"maxPriorityFeePerGas"`
	Paymaster          common.Address
	PaymasterData      []byte
	Signature          []byte
	EntryPoint         common.Address
	ChainId            *big.Int `rlp:"-"`
}

func (userOp UserOp) RequestId() (common.Hash, error) {
	userOp.Signature = []byte{}
	chainId := userOp.ChainId
	entryPointAddr := userOp.EntryPoint

	userOpAbiType, err := abi.NewType("tuple", "UserOperation", []abi.ArgumentMarshaling{
		{Name: "sender", Type: "address"},
		{Name: "nonce", Type: "uint256"},
		{Name: "initCode", Type: "bytes"},
		{Name: "callData", Type: "bytes"},
		{Name: "callGas", Type: "uint256"},
		{Name: "verificationGas", Type: "uint256"},
		{Name: "preVerificationGas", Type: "uint256"},
		{Name: "maxFeePerGas", Type: "uint256"},
		{Name: "maxPriorityFeePerGas", Type: "uint256"},
		{Name: "paymaster", Type: "address"},
		{Name: "paymasterData", Type: "bytes"},
		{Name: "signature", Type: "bytes"},
	})
	if err != nil {
		return common.Hash{}, err
	}
	userOpAbiArgs := abi.Arguments{abi.Argument{Name: "userOp", Type: userOpAbiType}}
	packedUserOp, err := userOpAbiArgs.Pack(userOp)
	if err != nil {
		return common.Hash{}, err
	}
	packedUserOp = packedUserOp[32 : len(packedUserOp)-32]
	hashedUserOp := crypto.Keccak256Hash(packedUserOp)

	bytes32AbiType, _ := abi.NewType("bytes32", "", nil)
	addressAbiType, _ := abi.NewType("address", "", nil)
	uint256AbiType, _ := abi.NewType("uint256", "", nil)
	requestIdAbiArgs := abi.Arguments{
		abi.Argument{Name: "userOp", Type: bytes32AbiType},
		abi.Argument{Name: "entryPoint", Type: addressAbiType},
		abi.Argument{Name: "chainId", Type: uint256AbiType},
	}
	requestId, err := requestIdAbiArgs.Pack(hashedUserOp, entryPointAddr, chainId)
	if err != nil {
		return common.Hash{}, err
	}

	return crypto.Keccak256Hash(requestId), nil
}
