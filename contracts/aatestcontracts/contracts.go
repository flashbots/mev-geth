package aatestcontracts

import (
	"encoding/json"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

//go:generate bash ./generate.sh

type AATestContract struct {
	Abi      abi.ABI
	Bytecode hexutil.Bytes
}

func NewAATestContract(artifact string) AATestContract {
	reader := strings.NewReader(artifact)
	dec := json.NewDecoder(reader)

	var contract AATestContract
	if err := dec.Decode(&contract); err != nil {
		panic(err)
	}
	return contract
}

func (contract *AATestContract) PackDeploy(args ...interface{}) ([]byte, error) {
	packedArgs, err := contract.Abi.Pack("", args...)
	return append(contract.Bytecode, packedArgs...), err
}

type SimpleWalletContract AATestContract

func (contract *SimpleWalletContract) PackDeploy(args ...interface{}) ([]byte, error) {
	packedArgs, err := contract.Abi.Pack("", args...)
	return append(contract.Bytecode, packedArgs...), err
}

type SingletonFactoryContract AATestContract

func (contract *SingletonFactoryContract) PackDeploy(args ...interface{}) ([]byte, error) {
	packedArgs, err := contract.Abi.Pack("", args...)
	return append(contract.Bytecode, packedArgs...), err
}

type TestCounterContract AATestContract

func (contract *TestCounterContract) PackDeploy(args ...interface{}) ([]byte, error) {
	packedArgs, err := contract.Abi.Pack("", args...)
	return append(contract.Bytecode, packedArgs...), err
}

type TokenPaymasterContract AATestContract

func (contract *TokenPaymasterContract) PackDeploy(args ...interface{}) ([]byte, error) {
	packedArgs, err := contract.Abi.Pack("", args...)
	return append(contract.Bytecode, packedArgs...), err
}

var (
	SimpleWallet     = SimpleWalletContract(NewAATestContract(simpleWalletArtifact))
	SingletonFactory = SingletonFactoryContract(NewAATestContract(singletonFactoryArtifact))
	TestCounter      = TestCounterContract(NewAATestContract(testCounterArtifact))
	TokenPaymaster   = TokenPaymasterContract(NewAATestContract(tokenPaymasterArtifact))
)
