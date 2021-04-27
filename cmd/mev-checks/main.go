package main

import (
	"context"
	"crypto/ecdsa"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	miner              = "0xd912aecb07e9f4e1ea8e6b4779e7fb6aa1c3e4d8"
	plainPayerContract = `
// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.7.0;

contract Bribe {
    function bribe() payable public {
        block.coinbase.transfer(msg.value);
    }
}
`
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
	keys = []*ecdsa.PrivateKey{k1}
)

func mbTxList() types.Transactions {
	txs := make(types.Transactions, len(keys))
	for i, key := range keys {
		// txs[i] = types.NewTransaction(
		// 		nonce uint64,
		// 		to common.Address,
		// 		amount *big.Int,
		// 		gasLimit uint64,
		// 		gasPrice *big.Int,
		// 		data []byte,
		// 	)
	}
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
