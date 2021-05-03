package miner

import (
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ProxyABIString is the input ABI used to generate the binding from.
const ProxyABIString = "[{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"coinbase\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"msgSender\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"FlashbotsPayment\",\"type\":\"event\"},{\"inputs\":[],\"name\":\"payMiner\",\"outputs\":[],\"stateMutability\":\"payable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"queueEther\",\"outputs\":[],\"stateMutability\":\"payable\",\"type\":\"function\"},{\"stateMutability\":\"payable\",\"type\":\"receive\"}]"

// ProxyFlashbotsPayment represents a FlashbotsPayment event raised by the Proxy contract.
type ProxyFlashbotsPayment struct {
	Coinbase  common.Address
	MsgSender common.Address
	Amount    *big.Int
	Raw       types.Log // Blockchain specific contextual infos
}

var (
	proxyPaymentTopic = common.HexToHash("0x82bfd7d226ef75398f858bca413814d37886af582526e7fae712e36fe8a5d297")
	proxyABI, _       = abi.JSON(strings.NewReader(ProxyABIString))
)
