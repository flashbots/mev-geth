# MEV-AA-geth

This is a fork of MEV-geth, which is a fork of go-ethereum, [the original README is here](README.original.md).

This repository aims to implement [ERC-4337](https://eips.ethereum.org/EIPS/eip-4337) on top of MEV-geth.
This repository specifically is maintained by Nethermind, however OpenGSN has [provided useful tooling](https://github.com/eth-infinitism/account-abstraction) alongside their EntryPoint.sol implementation, including an ethers.js fork that enables seamless signing with ERC-4337 compatible User Operation objects.

## Quick start

```
git clone https://github.com/nethermind-eth/mev-aa-geth
cd mev-aa-geth
make geth
```

See [here](https://geth.ethereum.org/docs/install-and-build/installing-geth#build-go-ethereum-from-source-code) for further info on building MEV-geth from source.

## Documentation

Further documentation coming soon, until then [ERC-4337](https://eips.ethereum.org/EIPS/eip-4337) will be the best resource to learn more about the functionality of this repository.

### Feature requests and bug reports

If you would like to contribute or report bugs, we appreciate the opening of any issues or pull requests.