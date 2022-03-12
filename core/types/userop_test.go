package types

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
)

func TestUserOpEncoding(t *testing.T) {
	op := &UserOp{
		Sender:             common.HexToAddress("0x01"),
		Nonce:              big.NewInt(1000),
		CallData:           []byte{1, 2},
		InitCode:           []byte{3, 4},
		CallGas:            big.NewInt(5),
		VerificationGas:    big.NewInt(6),
		PreVerificationGas: big.NewInt(7),
		GasFeeCap:          big.NewInt(8),
		GasTipCap:          big.NewInt(1),
		Paymaster:          common.HexToAddress("0x02"),
		PaymasterData:      []byte{5, 6},
		Signature:          []byte{1, 2, 3},
		EntryPoint:         common.HexToAddress("0x90f3e1105e63c877bf9587de5388c23cdb702c6b"),
		ChainId:            big.NewInt(5), // this should be ignored in rlp encoding
	}

	encoded, err := rlp.EncodeToBytes(op)
	if err != nil {
		t.Fatalf("Failed to encode %v: %v", op, err)
	}

	wants := "f8549400000000000000000000000000000000000000018203e88203048201020506070801940000000000000000000000000000000000000002820506830102039490f3e1105e63c877bf9587de5388c23cdb702c6b"
	// Meaning of RLP above:
	// f8
	// 54 = 84 (length of UserOp)
	// 94 = 148 (prefix of address)
	// 0000000000000000000000000000000000000001 (sender)
	// 82 = 130 = 128 + 2 (length of Nonce)
	// 03e8 = 1000 (Nonce)
	// 82 = 130 = 128 + 2 (length of InitCode)
	// 0304 (InitCode)
	// 82 = 130 = 128 + 2 (length of CallData)
	// 0102 (CallData)
	// 05 (CallGas)
	// 06 (VerificationGas)
	// 07 (PreVerificationGas
	// 08 (GasFeeCap)
	// 01 (GasTipCap)
	// 94 = 148 (prefix of address)
	// 0000000000000000000000000000000000000002 (Paymaster address)
	// 82 = 130 = 128 + 2 (length of PaymasterData)
	// 0506 (PaymasterData)
	// 83 = 131 = 128 + 3 (length of Signature)
	// 010203 (Signature)
	// 94 = 148 (prefix of address)
	// 90f3e1105e63c877bf9587de5388c23cdb702c6b (EntryPoint address)

	if !bytes.Equal(encoded, common.Hex2Bytes(wants)) {
		t.Fatalf("Wrong encoding (have: %v, wants: %v)", common.Bytes2Hex(encoded), wants)
	}
}
