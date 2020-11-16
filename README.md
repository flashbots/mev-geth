## MEV-Geth

This is a fork of go-ethereum, [the original README is here](README.original.md).

Flashbots is a research and development organization formed to mitigate the negative externalities the miner-extractable value (MEV) crisis poses to smart-contract-based blockchains by creating an open-entry, transparent and fair marketplace for MEV extraction, starting with Ethereum.

To fix this problem, we have designed and implemented a proof of concept for permissionless MEV extraction called MEV-Extract. It is a sealed-bid block space auction mechanism that aims to obviate the use of frontrunning and backrunning techniques.

## MEV-Extract: a proof of concept

We have designed and implemented a proof of concept for permissionless MEV extraction called MEV-Extract. It is a sealed-bid block space auction mechanism for communicating transaction order preference. While our proof of concept has incomplete trust guarantees, we believe it's a big improvement over the status quo. Adoption of MEV-Extract should relieve a lot of the network and chain congestion caused by frontrunning and backrunning bots.

| Guarantee            | PGA | DarkPool | mev-extract |
| -------------------- | --- | -------- | ----------- |
| Permissionless       | ✅  | ❌       | ✅          |
| Efficient            | ❌  | ❌       | ✅          |
| Pre-trade privacy    | ❌  | ✅       | ✅          |
| Failed trade privacy | ❌  | ❌       | ✅          |
| Complete privacy     | ❌  | ❌       | ❌          |
| Finality             | ❌  | ❌       | ❌          |

### Why MEV-Extract?

We believe that without the adoption of neutral, public, open-source-licensed infrastructure for permissionless MEV extraction, MEV risks becoming an insiders' game. It is thus our intent as an organization to commit to releasing reference implementations for participation in fair, ethical, and politically netural MEV extraction, preventing the properties of Ethereum from being eroded by trust-based dark pools or proprietary channels, which also serve as key points of security weakness. We thus release MEV-Extract with the dual goal of preserving Ethereum properties and of creating a sustainable long-term marketplace, as well as with the intent of starting conversations with the community around our research and development agenda.

### Design goals

- **Permissionless**  
A permissionless design implies there are no trusted intermediary which can censor transactions. 

- **Efficient**  
An efficient design implies MEV extraction is performed without causing unnecessary network or chain congestion.

- **Pre-trade privacy**  
Pre-trade privacy implies transactions only become publicaly known after they have been included in a block. *Note, this type of privacy does not exclude privileged actors such as transaction aggregators / gateways / miners.*

- **Failed trade privacy**  
Failed trade privacy implies loosing bids are never included in a block, thus never exposed to the public. Failed trade privacy is tightly coupled to extraction efficiency.

- **Complete privacy**  
Complete privacy implies there are no privileged actors such as transaction aggregators / gateways / miners who can observe incoming transactions.

The MEV-Extract proof of concept relies on the fact that searchers can withold bids from certain miners in order to disincentivize bad behavior like stealing a profitable strategy. We expect a complete privacy design to necessitate some sort of private computation solution like SGX, ZKP, or MPC to withhold the transaction content from miners until mined in a block. One of the core objective of the flashbots organization is to incentivize research in this direction.

- **Finality**  
Finality implies it is infeasible for MEV extraction to be reversed once included in a block. This would protect against time-bandit chain re-org attacks.

The MEV-Extract proof of concept does not provide any finality guatantee. We expect the solution to this problem to require post-trade execution privacy through private chain state or strong economic infeasibility. The design of a system with strong finality is the second core objective of the flashbots organization. 

### How it works

MEV-Extract introduces the concepts of "searchers", "transaction bundles", and "block template" to Ethereum. Effectively, MEV-Extract provides a way for miners to delegate the task of finding and ordering transactions to third parties called "searchers". These searchers compete with eachother to find the most profitable ordering and bid for its inclusion in the next block using a standardized template called a "transaction bundle". These bundles are evaluated in a sealed-bid auction hosted by miners to produce a "block template" which holds the information about transaction order required to begin mining.

<img width="1299" alt="Screen Shot 2020-11-16 at 6 59 49 PM" src="https://user-images.githubusercontent.com/15959632/99290073-1b895300-283e-11eb-8687-bdfbe710f753.png">

The MEV-Extract proof of concept is compatible with any regular Ethereum client. MEV-Geth serves as the reference implementation and has the following four components:

- **Bundle RPC**  
A new RPC method called `eth_sendBundle` is used to receive transaction bundles from searchers.

- **Bundle Pool**  
Bundles are validated and placed in a local bundle pool. Local bundles are not gossiped to the rest of the network.

- **Bundle Auction**  
The client selects the most profitable bundle from the pool and includes it at the begining of the list of transactions to be included in the next block. The remaining block space is then filled with transactions from the transaction pool.

- **Profit Switching**  
In parallel, the client produces a "normal" block template based on the regular transaction pool and gas auction. Finally, the client compares the revenue of each template (normal vs MEV-Extract) and switches to mining on the most profitable one. This last step ensures that at worst, miners running MEV-Extract end up with the status quo.

### How to use as a searcher

A searcher's job is to monitor the ethereum state and transaction pool for MEV opportunities and produce transaction bundles that extract that MEV. Anyone can become a searcher. In fact, the bundles produced by searchers don't need to extract MEV at all, but we expect the most valuable bundles will. An MEV-Extract bundle is a standard message template composed of an array of valid ethereum transactions, a blockheight, and an optional timestamp range over which the bundle is valid.

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

MEV-Extract miners select the most profitable bundle per unit of gas used and place it at the beginning of the list of transactions of the block template at a given blockheight. Miners determine the value of a bundle based on the following equation. *Note, the change in block.coinbase balance represents a direct transfer of ETH through a smart contract.*

<img width="544" alt="Screen Shot 2020-11-16 at 7 02 27 PM" src="https://user-images.githubusercontent.com/15959632/99290205-470c3d80-283e-11eb-8ccd-d60d8abf39aa.png">

To submit a bundle, the searcher sends the bundle directly to the miner using the rpc method `eth_sendBundle`. Since MEV-Extract requires direct communication between searchers and miners, a searcher can configure the list of miners where they want to send their bundle.

### How to use as a miner

Miners can start mining MEV blocks by running MEV-Geth or by implementing their own fork that matches the specification.

In order to start receiving bundles from searchers, miners will need to publish a public https endpoint that exposes the `eth_sendBundle` RPC.

### Moving beyond proof of concept
We provide the MEV-Extract proof of concept as a first milestone on the path to mitigating the negative externalities caused by MEV. We hope to discuss with the community the merits of adopting MEV-Extract in its current form. Our preliminary research indicates it could free at least 12% of the current chain congestion and provide uplift of at least 3% on miner rewards by eliminating the use of frontrunning and backrunning from Ethereum. That being said, we believe a sustainable solution to MEV existential risks requires complete privacy and finality, which the proof of concept does not address. We hope to engage community feedback throughout the development of this complete version of MEV-Extract.
