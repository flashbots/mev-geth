package entrypoint

import (
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

var (
	entryPointAddr = common.HexToAddress("0x6C87B76AfA19823F423fA3cAf3a550a967181775")
)

type EntryPointContract struct {
	address common.Address
	abi     abi.ABI
}

func NewEntryPointContract() (*EntryPointContract, error) {
	reader := strings.NewReader(entrypointAbiString)
	abi, err := abi.JSON(reader)
	if err != nil {
		return nil, err
	}

	return &EntryPointContract{
		address: entryPointAddr,
		abi:     abi,
	}, nil
}

func (entrypoint *EntryPointContract) Address() common.Address {
	return entrypoint.address
}

func (entrypoint *EntryPointContract) ABI() abi.ABI {
	return entrypoint.abi
}

func (entrypoint *EntryPointContract) PackSimulateValidation(arg interface{}) ([]byte, error) {
	return entrypoint.abi.Pack("simulateValidation", arg)
}
