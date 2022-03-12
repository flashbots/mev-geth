package eth

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/protocols/aa"
	"github.com/ethereum/go-ethereum/p2p/enode"
)

// aaHandler implements the aa.Backend interface to handle the various network
// packets that are sent as replies or broadcasts.
type aaHandler handler

func (h *aaHandler) ChainID() *big.Int         { return h.chain.Config().ChainID }
func (h *aaHandler) UserOpPool() aa.UserOpPool { return h.oppool }

// RunPeer is invoked when a peer joins on the `aa` protocol.
func (h *aaHandler) RunPeer(peer *aa.Peer, hand aa.Handler) error {
	return (*handler)(h).runAAExtension(peer, hand)
}

// PeerInfo retrieves all known `aa` information about a peer.
func (h *aaHandler) PeerInfo(id enode.ID) interface{} {
	if p := h.peers.peer(id.String()); p != nil {
		return p.info()
	}
	return nil
}

// AcceptOps retrieves whether userop processing is enabled on the node
// or if inbound userops should simply be dropped.
func (h *aaHandler) AcceptOps() bool {
	// TODO(jmederos): probably check if we're bundling/mining or even accepting txs
	return true
}

// Handle is invoked from a peer's message handler when it receives a new remote
// message that the handler couldn't consume and serve itself.
func (h *aaHandler) Handle(peer *aa.Peer, packet aa.Packet) error {
	switch packet := packet.(type) {
	case *aa.UserOpsPacket:
		return h.addUserOps(*packet)
	default:
		return fmt.Errorf("unexpected aa packet type: %T", packet)
	}
}

func (h *aaHandler) addUserOps(ops []*types.UserOp) error {
	for _, op := range ops {
		h.oppool.AddUserOp(op)
	}
	return nil
}
