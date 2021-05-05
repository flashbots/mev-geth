package main

import (
	"context"
	"crypto/ecdsa"
	"flag"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
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
	bribeContractABI = `[
    {
      inputs: [],
      name: 'bribe',
      outputs: [],
      stateMutability: 'payable',
      type: 'function'
    }
  ]
`
)

var (
	clientDial = flag.String(
		"client_dial", "ws://127.0.0.1:8546", "could be websocket or IPC",
	)
	cb = flag.String(
		"coinbase", miner, "what coinbase to use",
	)
	at        = flag.Uint64("kickoff", 32, "what number to kick off at")
	faucet, _ = crypto.HexToECDSA(
		"133be114715e5fe528a1b8adf36792160601a2d63ab59d1fd454275b31328791",
	)
	keys        = []*ecdsa.PrivateKey{faucet}
	bribeABI, _ = abi.JSON(strings.NewReader(string(bribeContractABI)))
)

func mbTxList(
	client *ethclient.Client,
	toAddr common.Address,
	chainID *big.Int,
) (types.Transactions, error) {

	packed, err := bribeABI.Methods["bribe"].Inputs.Pack()
	if err != nil {
		return nil, err
	}
	txs := make(types.Transactions, len(keys))

	for i, key := range keys {
		k := crypto.PubkeyToAddress(key.PublicKey)
		non, err := client.NonceAt(
			context.Background(), k, nil,
		)
		if err != nil {
			return nil, err
		}

		balance, err := client.BalanceAt(context.Background(), k, nil)
		if err != nil {
			return nil, err
		}
		fmt.Println("public key is ", k.Hex(), "balance", balance)

		t := types.NewTransaction(
			non,
			toAddr,
			new(big.Int),
			100_000,
			big.NewInt(3e9),
			packed,
		)
		t, err = types.SignTx(t, types.NewEIP155Signer(chainID), key)
		if err != nil {
			return nil, err
		}
		txs[i] = t
	}
	return txs, nil
}

func deployBribeContract(
	client *ethclient.Client,
	chainID *big.Int,
) (*types.Transaction, error) {
	t := types.NewContractCreation(
		0, new(big.Int), 400_000, big.NewInt(10e9),
		common.Hex2Bytes(bribeContractBin),
	)

	t, err := types.SignTx(t, types.NewEIP155Signer(chainID), faucet)
	if err != nil {
		return nil, err
	}

	return t, client.SendTransaction(context.Background(), t)
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

	var (
		newContractAddr common.Address
		usedTxs         types.Transactions
	)

	chainID, err := client.ChainID(context.Background())
	if err != nil {
		return err
	}

	for {
		select {
		case e := <-sub.Err():
			return e
		case incoming := <-ch:
			blockNumber := incoming.Number.Uint64()
			if blockNumber == 15 {
				t, err := deployBribeContract(client, chainID)
				if err != nil {
					return err
				}

				newContractAddr = crypto.CreateAddress(
					crypto.PubkeyToAddress(faucet.PublicKey),
					t.Nonce(),
				)
				fmt.Println("deployed bribe contract ", newContractAddr.Hex(), blockNumber)
				continue
			}

			if blockNumber == *at {
				usedTxs, err := mbTxList(client, newContractAddr, chainID)
				if err != nil {
					return err
				}

				fmt.Println("using as parent hash",
					incoming.Hash().Hex(), incoming.Number,
				)

				if err := client.SendMegaBundle(
					context.Background(), &types.MegaBundle{
						TransactionList: usedTxs,
						Timestamp:       uint64(time.Now().Add(time.Second * 45).Unix()),
						CoinbaseDiff:    1e17,
						Coinbase:        common.HexToAddress(*cb),
						ParentHash:      incoming.Hash(),
					},
				); err != nil {
					return err
				}
				fmt.Println("kicked off mega bundle")
			}

			if blockNumber > *at {
				blk, err := client.BlockByNumber(context.Background(), incoming.Number)
				if err != nil {
					return err
				}
				for _, t := range blk.Transactions() {
					for _, t2 := range usedTxs {
						if t.Hash() == t2.Hash() {
							fmt.Println("our mega bundle tx was confirmed", t.Hash())
							return nil
						}
					}
				}
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
