package entrypoint

import (
	"encoding/json"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

type EntryPoint struct {
	Abi      abi.ABI
	Bytecode hexutil.Bytes
}

func NewEntryPoint() *EntryPoint {
	reader := strings.NewReader(entryPointArtifact)
	dec := json.NewDecoder(reader)

	var entryPoint EntryPoint
	if err := dec.Decode(&entryPoint); err != nil {
		panic(err)
	}
	return &entryPoint
}

func (entryPoint *EntryPoint) PackDeploy(create2Factory common.Address, perOpOverhead *big.Int, unstakeDelayBlocks *big.Int) ([]byte, error) {
	return entryPoint.Abi.Pack("", create2Factory, perOpOverhead, unstakeDelayBlocks)
}

func (entryPoint *EntryPoint) PackSimulateValidation(op *types.UserOp) ([]byte, error) {
	return entryPoint.Abi.Pack("simulateValidation", op.ToAbi())
}

func (entryPoint *EntryPoint) PackHandleOp(op *types.UserOp, redeemer common.Address) ([]byte, error) {
	return entryPoint.Abi.Pack("handleOp", op.ToAbi(), redeemer)
}

func (entryPoint *EntryPoint) PackHandleOps(ops []*types.UserOp, redeemer common.Address) ([]byte, error) {
	userOpsAbi := make([]*types.UserOpAbi, len(ops))
	for i := range ops {
		userOpsAbi[i] = ops[i].ToAbi()
	}
	return entryPoint.Abi.Pack("handleOps", userOpsAbi, redeemer)
}
