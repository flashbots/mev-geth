package main

import (
	"context"
	"crypto/ecdsa"
	"flag"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	miner = "0xd912aecb07e9f4e1ea8e6b4779e7fb6aa1c3e4d8"
	// SPDX-License-Identifier: UNLICENSED
	// pragma solidity ^0.7.0;
	// contract Bribe {
	//     function bribe() payable public {
	//         block.coinbase.transfer(msg.value);
	//     }
	// }
	bribeContractBin = `0x6080604052348015600f57600080fd5b5060a78061001e6000396000f3fe608060405260043610601c5760003560e01c806337d0208c146021575b600080fd5b60276029565b005b4173ffffffffffffffffffffffffffffffffffffffff166108fc349081150290604051600060405180830381858888f19350505050158015606e573d6000803e3d6000fd5b5056fea2646970667358221220862610b9326c9523da6465ba88229d2c6b26ff844b3c9cddb807c2c1ab401dd964736f6c63430007050033`
)

var (
	clientDial = flag.String(
		"client_dial", "ws://127.0.0.1:8546", "could be websocket or IPC",
	)
	cb = flag.String(
		"coinbase", miner, "what coinbase to use",
	)
	at    = flag.Uint64("kickoff", 64, "what number to kick off at")
	k1, _ = crypto.HexToECDSA(
		"",
	)
	faucet, _ = crypto.HexToECDSA(
		"133be114715e5fe528a1b8adf36792160601a2d63ab59d1fd454275b31328791",
	)
	keys = []*ecdsa.PrivateKey{k1, faucet}
)

func mbTxList() types.Transactions {
	var txs types.Transactions
	// test the contract creation way -
	// also might be in megabundle right

	t := types.NewContractCreation(
		0, new(big.Int), 200_000, big.NewInt(10e9), common.Hex2Bytes(bribeContractBin),
	)
	t, _ = types.SignTx(t, types.NewEIP155Signer(big.NewInt(1)), faucet)
	txs = append(txs, t)
	// txs := make(types.Transactions, len(keys))
	for i, key := range keys {
		_ = i
		_ = key
		// txs[i] = types.NewTransaction(
		// 		nonce uint64,
		// 		to common.Address,
		// 		amount *big.Int,
		// 		gasLimit uint64,
		// 		gasPrice *big.Int,
		// 		data []byte,
		// 	)
	}
	// txs = append(txs, )
	return txs
}

func program() error {
	client, err := ethclient.Dial(*clientDial)
	if err != nil {
		return err
	}

	ch := make(chan *types.Header)
	sub, err := client.SubscribeNewHead(
		context.Background(), ch,
	)

	if err != nil {
		return err
	}

	for {
		select {
		case e := <-sub.Err():
			return e
		case incoming := <-ch:
			fmt.Println("header is ", incoming)
			if incoming.Number.Uint64() == *at {
				if err := client.SendMegaBundle(
					context.Background(), &types.MegaBundle{
						TransactionList: mbTxList(),
						Timestamp:       uint64(time.Now().Add(time.Second * 45).Unix()),
						Coinbase_diff:   3e18,
						Coinbase:        common.HexToAddress(*cb),
						ParentHash:      incoming.Root,
					},
				); err != nil {
					return err
				}
				fmt.Println("kicked off mega bundle")
			}
		}
	}
}

func main() {
	flag.Parse()
	if err := program(); err != nil {
		log.Fatal(err)
	}
}
