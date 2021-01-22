# MEV-geth

This is a fork of go-ethereum, [the original README is here](README.original.md).

Flashbots is a research and development organization formed to mitigate the negative externalities and existential risks posed by miner-extractable value (MEV) to smart-contract blockchains. We propose a permissionless, transparent, and fair ecosystem for MEV extraction that reinforce the Ethereum ideals.

## Quick start
```
git clone https://github.com/flashbots/mev-geth
cd mev-geth
make geth
```
See [here](https://geth.ethereum.org/docs/install-and-build/installing-geth#build-go-ethereum-from-source-code) for further info on building MEV-geth from source.

## MEV-Geth: a proof of concept

We have designed and implemented a proof of concept for permissionless MEV extraction called MEV-Geth. It is a sealed-bid block space auction mechanism for communicating transaction order preference. While our proof of concept has incomplete trust guarantees, we believe it's a significant improvement over the status quo. The adoption of MEV-Geth should relieve a lot of the network and chain congestion caused by frontrunning and backrunning bots.

| Guarantee            | PGA | Dark-txPool | MEV-Geth |
| -------------------- |:---:|:--------:|:-----------:|
| Permissionless       | ✅  |    ❌    |     ✅      |
| Efficient            | ❌  |    ❌    |     ✅      |
| Pre-trade privacy    | ❌  |    ✅    |     ✅      |
| Failed trade privacy | ❌  |    ❌    |     ✅      |
| Complete privacy     | ❌  |    ❌    |     ❌      |
| Finality             | ❌  |    ❌    |     ❌      |

### Why MEV-Geth?

We believe that without the adoption of neutral, public, open-source infrastructure for permissionless MEV extraction, MEV risks becoming an insiders' game. We commit as an organization to releasing reference implementations for participation in fair, ethical, and politically neutral MEV extraction. By doing so, we hope to prevent the properties of Ethereum from being eroded by trust-based dark pools or proprietary channels which are key points of security weakness. We thus release MEV-Geth with the dual goal of creating an ecosystem for MEV extraction that preserves Ethereum properties, as well as starting conversations with the community around our research and development roadmap.

### Design goals

- **Permissionless**
A permissionless design implies there are no trusted intermediary which can censor transactions.
- **Efficient**
An efficient design implies MEV extraction is performed without causing unnecessary network or chain congestion.
- **Pre-trade privacy**
Pre-trade privacy implies transactions only become publicly known after they have been included in a block. Note, this type of privacy does not exclude privileged actors such as transaction aggregators / gateways / miners.
- **Failed trade privacy**
Failed trade privacy implies loosing bids are never included in a block, thus never exposed to the public. Failed trade privacy is tightly coupled to extraction efficiency.
- **Complete privacy**
Complete privacy implies there are no privileged actors such as transaction aggregators / gateways / miners who can observe incoming transactions.
- **Finality**
Finality implies it is infeasible for MEV extraction to be reversed once included in a block. This would protect against time-bandit chain re-org attacks.

The MEV-Geth proof of concept relies on the fact that searchers can withhold bids from certain miners in order to disincentivize bad behavior like stealing a profitable strategy. We expect a complete privacy design to necessitate some sort of private computation solution like SGX, ZKP, or MPC to withhold the transaction content from miners until it is mined in a block. One of the core objective of the Flashbots organization is to incentivize and produce research in this direction.

The MEV-Geth proof of concept does not provide any finality guarantees. We expect the solution to this problem to require post-trade execution privacy through private chain state or strong economic infeasibility. The design of a system with strong finality is the second core objective of the MEV-Geth research effort.

### How it works

MEV-Geth introduces the concepts of "searchers", "transaction bundles", and "block template" to Ethereum. Effectively, MEV-Geth provides a way for miners to delegate the task of finding and ordering transactions to third parties called "searchers". These searchers compete with each other to find the most profitable ordering and bid for its inclusion in the next block using a standardized template called a "transaction bundle". These bundles are evaluated in a sealed-bid auction hosted by miners to produce a "block template" which holds the [information about transaction order required to begin mining](https://ethereum.stackexchange.com/questions/268/ethereum-block-architecture).

![](https://hackmd.io/_uploads/B1fWz7rcD.png)

The MEV-Geth proof of concept is compatible with any regular Ethereum client. The Flashbots core devs are maintaining [a reference implementation](https://github.com/flashbots/mev-geth) for the go-ethereum client.

### What is the difference between MEV-Geth and geth

An in-depth list of the changes can be seen by inspecting the [diff](https://github.com/ethereum/go-ethereum/compare/master...flashbots:master#diff-c426ecd2f7d247753b9ea8c1cc003f21fa412661c1f967d203d4edf8163da344R1970). Summary below:

- Modify Geth’s txpool to also contain a `mevBundles` field, which stores a list of MEV bundles. Each MEV bundle is an array of transactions, along with a min/max timestamp for their inclusion.
- A new eth_sendBundle API is exposed which allows adding an MEV Bundle to the txpool. This is called by traders.
  - The transactions submitted to the bundle are “eth_sendRawTransaction-style” RLP encoded signed transactions along with the min/max block of inclusion
  - This API is a no-op when run in light mode
- Geth’s miner is modified as follows:
  - While in the event loop, before adding all the pending txpool “normal” transactions to the block, it:
    - Finds the most profitable bundle
      - It picks the most profitable bundle by returning the one with the highest average gas price per unit of gas
        - computeBundleGas: Returns average gas price (\sum{gasprice_i*gasused_i + (coinbase_after - coinbase_before)) / \sum{gasused_i})
    - Commits the bundle(remember: Bundle transactions are not ordered by nonce or gas price). For each transaction in the bundle, it:
        - `Prepare`’s it against the state
        - CommitsTransaction with trackProfit = true
            w.current.profit += coinbase_after_tx - coinbase_before_tx
            w.current.profit += gas * gas_price
  - If a block is found where the w.current.profit is more than the previous profit, it switches mining to that block.


### How to use as a miner

Miners can start mining MEV blocks by running MEV-Geth or by implementing their own fork that matches the specification.

In order to start receiving bundles from searchers, miners will need to publish a [public https endpoint that exposes the `eth_sendBundle` RPC](https://github.com/flashbots/mev-relay-js).

MEV-Geth is maintained by the Flashbots core dev team and [the source code can be found on github](https://github.com/flashbots/mev-geth).

### How to use as a searcher

A searcher's job is to monitor the Ethereum state and transaction pool for MEV opportunities and produce transaction bundles that extract that MEV. Anyone can become a searcher. In fact, the bundles produced by searchers don't need to extract MEV at all, but we expect the most valuable bundles will. An MEV-Geth bundle is a standard message template composed of an array of valid ethereum transactions, a blockheight, and an optional timestamp range over which the bundle is valid.

```jsonld
{
    "signedTransactions": ['...'], // RLP encoded signed transaction array
    "blocknumber": "0x386526", // hex string
    "minTimestamp": 12345, // optional uint64
    "maxTimestamp": 12345 // optional uint64
}
```

The `signedTransactions` can be any valid ethereum transactions. Care must be taken to place transaction nonces in correct order.

The `blocknumber` defines the block height at which the bundle is to be included. A bundle will only be evaluated for the provided blockheight and immediately evicted if not selected.

The `minTimestamp` and `maxTimestamp` are optional conditions to further restrict bundle validity within a time range.

MEV-Geth miners select the most profitable bundle per unit of gas used and place it at the beginning of the list of transactions of the block template at a given blockheight. Miners determine the value of a bundle based on the following equation. *Note, the change in block.coinbase balance represents a direct transfer of ETH through a smart contract.*

<img width="544" src="https://hackmd.io/_uploads/Bk6iQmr5P.png">

To submit a bundle, the searcher sends the bundle directly to the miner using the rpc method `eth_sendBundle`. Since MEV-Geth requires direct communication between searchers and miners, a searcher can configure the list of miners where they want to send their bundle.

### Moving beyond proof of concept
We provide the MEV-Geth proof of concept as a first milestone on the path to mitigating the negative externalities caused by MEV. We hope to discuss with the community the merits of adopting MEV-Geth in its current form. Our preliminary research indicates it could free at least 2.5% of the current chain congestion by eliminating the use of frontrunning and backrunning and provide uplift of up to 18% on miner rewards from Ethereum. That being said, we believe a sustainable solution to MEV existential risks requires complete privacy and finality, which the proof of concept does not address. We hope to engage community feedback throughout the development of this complete version of MEV-Geth.
