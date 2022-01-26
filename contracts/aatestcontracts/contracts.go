package aatestcontracts

import (
	"encoding/json"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
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

func (contract *SimpleWalletContract) PackDeploy(entryPointAddr common.Address, owner common.Address) ([]byte, error) {
	packedArgs, err := contract.Abi.Pack("", entryPointAddr, owner)
	return append(contract.Bytecode, packedArgs...), err
}

func (contract *SimpleWalletContract) PackExecFromEntryPoint(dest common.Address, value *big.Int, callData []byte) ([]byte, error) {
	return contract.Abi.Pack("execFromEntryPoint", dest, common.Big0, callData)
}

type SingletonFactoryContract AATestContract

func (contract *SingletonFactoryContract) PackDeploy() ([]byte, error) {
	return contract.Bytecode, nil
}

type TestCounterContract AATestContract

func (contract *TestCounterContract) PackDeploy() ([]byte, error) {
	return contract.Bytecode, nil
}

func (contract *TestCounterContract) PackCount() ([]byte, error) {
	return contract.Abi.Pack("count")
}

type TokenPaymasterContract AATestContract

func (contract *TokenPaymasterContract) PackDeploy(symbol string, entryPointAddr common.Address) ([]byte, error) {
	packedArgs, err := contract.Abi.Pack("", symbol, entryPointAddr)
	return append(contract.Bytecode, packedArgs...), err
}

var (
	SimpleWallet     = SimpleWalletContract(NewAATestContract(simpleWalletArtifact))
	SingletonFactory = SingletonFactoryContract(NewAATestContract(singletonFactoryArtifact))
	TestCounter      = TestCounterContract(NewAATestContract(testCounterArtifact))
	TokenPaymaster   = TokenPaymasterContract(NewAATestContract(tokenPaymasterArtifact))
)
