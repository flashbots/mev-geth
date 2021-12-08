package entrypoint

import (
	"os"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

type EntryPointContract struct {
	address common.Address
	abi     abi.ABI
}

func NewEntryPointContract() (*EntryPointContract, error) {
	reader, err := os.Open("./entrypoint.json")
	if err != nil {
		return nil, err
	}

	abi, err := abi.JSON(reader)
	if err != nil {
		return nil, err
	}

	return &EntryPointContract{
        address: common.HexToAddress("0x6C87B76AfA19823F423fA3cAf3a550a967181775"),
        abi: abi,
    }, nil
}

func (entrypoint *EntryPointContract) Address() common.Address {
	return entrypoint.address
}

func (entrypoint *EntryPointContract) ABI() abi.ABI {
	return entrypoint.abi
}
