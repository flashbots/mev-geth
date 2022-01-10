package entrypoint

import (
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type EntryPointAbi struct {
	abi abi.ABI
}

func NewEntryPointAbi() *EntryPointAbi {
	reader := strings.NewReader(entrypointAbiString)
	abi, _ := abi.JSON(reader)

	return &EntryPointAbi{
		abi: abi,
	}
}

func (entrypoint *EntryPointAbi) PackSimulateValidation(op *types.UserOp) ([]byte, error) {
	return entrypoint.abi.Pack("simulateValidation", op.ToAbi())
}

func (entrypoint *EntryPointAbi) PackHandleOp(op *types.UserOp, redeemer common.Address) ([]byte, error) {
	return entrypoint.abi.Pack("handleOp", op.ToAbi(), redeemer)
}

func (entrypoint *EntryPointAbi) PackHandleOps(ops []*types.UserOp, redeemer common.Address) ([]byte, error) {
	userOpsAbi := make([]*types.UserOpAbi, len(ops))
	for i := range ops {
		userOpsAbi[i] = ops[i].ToAbi()
	}
	return entrypoint.abi.Pack("handleOps", userOpsAbi, redeemer)
}
