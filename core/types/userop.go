package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type UserOp struct {
	Nonce     uint64
	Signature []byte

	PreVerificationGas uint64
	VerificationGas    uint64
	CallGas            uint64
	GasTipCap          *big.Int
	GasFeeCap          *big.Int

	InitCode      []byte
	Sender        common.Address
	CallData      []byte
	Paymaster     common.Address
	PaymasterData []byte
}

type SimulatedUserOp struct {
	*UserOp

	AccessList AccessList
}

type UserOpAbi struct {
	Nonce     *big.Int
	Signature []byte

	PreVerificationGas   *big.Int
	VerificationGas      *big.Int
	CallGas              *big.Int
	MaxPriorityFeePerGas *big.Int
	MaxFeePerGas         *big.Int

	InitCode      []byte
	Sender        *common.Address
	CallData      []byte
	Paymaster     *common.Address
	PaymasterData []byte
}

func (userOp *UserOp) ToAbi() *UserOpAbi {
	return &UserOpAbi{
		Nonce:     new(big.Int).SetUint64(userOp.Nonce),
		Signature: userOp.Signature,

		PreVerificationGas:   new(big.Int).SetUint64(userOp.PreVerificationGas),
		VerificationGas:      new(big.Int).SetUint64(userOp.VerificationGas),
		CallGas:              new(big.Int).SetUint64(userOp.CallGas),
		MaxPriorityFeePerGas: userOp.GasTipCap,
		MaxFeePerGas:         userOp.GasFeeCap,

		InitCode:      userOp.InitCode,
		Sender:        &userOp.Sender,
		CallData:      userOp.CallData,
		Paymaster:     &userOp.Paymaster,
		PaymasterData: userOp.PaymasterData,
	}
}

func (userOp *UserOpAbi) RequestId(entryPointAddr common.Address, chainId *big.Int) (common.Hash, error) {
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
	userOpWithoutSig := *userOp
	userOpWithoutSig.Signature = []byte{}
	packedUserOp, err := userOpAbiArgs.Pack(userOpWithoutSig)
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

func (al1 AccessList) Overlaps(al2 AccessList) bool {
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

type alCacheEntry struct {
	idx   int
	slots map[common.Hash]bool
}
type alCache map[common.Address]alCacheEntry
