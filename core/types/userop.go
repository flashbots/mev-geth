package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
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

// TODO: this is not being used, maybe remove it
func (al *AccessList) containsAddr(addr common.Address, cache *alCache) bool {
	panic("Not Implemented")
}

// TODO: this is not being used, maybe remove it
func (al *AccessList) containsSlot(addr common.Address, cache *alCache) bool {
	panic("Not Implemented")
}

// TODO: this is not being used, maybe remove it
func (al1 *AccessList) Extend(al2 AccessList) {
	alCache := make(alCache)
	for idx, tuple := range *al1 {
		if _, present := alCache[tuple.Address]; !present {
			alCache[tuple.Address] = alCacheEntry{idx, make(map[common.Hash]bool)}
		}
		for _, slot := range tuple.StorageKeys {
			alCache[tuple.Address].slots[slot] = true
		}
	}
	for _, tuple := range al2 {
		if !al1.containsAddr(tuple.Address, &alCache) {
			*al1 = append(*al1, tuple)
		} else {
			slots := &(*al1)[alCache[tuple.Address].idx].StorageKeys
			slotsCache := alCache[tuple.Address].slots
			for _, slot := range tuple.StorageKeys {
				if _, present := slotsCache[slot]; !present {
					*slots = append(*slots, slot)
				}
			}
		}
	}
}

func (al1 AccessList) Equals(al2 AccessList) bool {
	return true
}
