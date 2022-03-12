package aa

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/enr"
)

// Handler is a callback to invoke from an outside runner after the boilerplate
// exchanges have passed.
type Handler func(peer *Peer) error

type Backend interface {
	// ChainID retrieves the current ChainID
	ChainID() *big.Int

	// UserOpPool retrieves the user operation pool object to serve data.
	UserOpPool() UserOpPool

	// AcceptOps retrieves whether userop processing is enabled on the node
	// or if inbound userops should simply be dropped.
	AcceptOps() bool

	// RunPeer is invoked when a peer joins on the `aa` protocol. The handler
	// should do any peer maintenance work, handshakes and validations. If all
	// is passed, control should be given back to the `handler` to process the
	// inbound messages going forward.
	RunPeer(peer *Peer, handler Handler) error

	// PeerInfo retrieves all known `aa` information about a peer.
	PeerInfo(id enode.ID) interface{}

	// Handle is a callback to be invoked when a data packet is received from
	// the remote peer. Only packets not consumed by the protocol handler will
	// be forwarded to the backend.
	Handle(peer *Peer, packet Packet) error
}

// UserOpPool defines the methods needed by the protocol handler to serve userops.
type UserOpPool interface {
	// Get retrieves the transaction from the local oppool with the given hash.
	Get(hash common.Hash) *types.UserOp
}

// MakeProtocols constructs the P2P protocol definitions for `aa`.
func MakeProtocols(backend Backend, dnsdisc enode.Iterator) []p2p.Protocol {
	// Filter the discovery iterator for nodes advertising aa support.
	dnsdisc = enode.Filter(dnsdisc, func(n *enode.Node) bool {
		var aa enrEntry
		return n.Load(&aa) == nil
	})

	protocols := make([]p2p.Protocol, len(ProtocolVersions))
	for i, version := range ProtocolVersions {
		version := version // Closure

		protocols[i] = p2p.Protocol{
			Name:    ProtocolName,
			Version: version,
			Length:  protocolLengths[version],
			Run: func(p *p2p.Peer, rw p2p.MsgReadWriter) error {
				peer := newPeer(version, p, rw, backend.UserOpPool())
				defer peer.Close()

				return backend.RunPeer(peer, func(peer *Peer) error {
					return handle(backend, peer)
				})
			},
			NodeInfo: func() interface{} {
				return nodeInfo()
			},
			PeerInfo: func(id enode.ID) interface{} {
				return backend.PeerInfo(id)
			},
			Attributes:     []enr.Entry{&enrEntry{}},
			DialCandidates: dnsdisc,
		}
	}
	return protocols
}

// NodeInfo represents a short summary of the `aa` sub-protocol metadata
// known about the host peer.
type NodeInfo struct {
}

// nodeInfo retrieves some `aa` protocol metadata about the running host node.
func nodeInfo() *NodeInfo {
	return &NodeInfo{}
}

// handle is the callback invoked to manage the life cycle of a `snap` peer.
// When this function terminates, the peer is disconnected.
func handle(backend Backend, peer *Peer) error {
	for {
		if err := handleMessage(backend, peer); err != nil {
			peer.Log().Debug("Message handling failed in `aa`", "err", err)
			return err
		}
	}
}

type msgHandler func(backend Backend, msg Decoder, peer *Peer) error
type Decoder interface {
	Decode(val interface{}) error
	Time() time.Time
}

var aa0 = map[uint64]msgHandler{
	UserOpsMsg: handleUserOps,
}

// handleMessage is invoked whenever an inbound message is received from a remote
// peer. The remote connection is torn down upon returning any error.
func handleMessage(backend Backend, peer *Peer) error {
	// Read the next message from the remote peer, and ensure it's fully consumed
	msg, err := peer.rw.ReadMsg()
	if err != nil {
		return err
	}
	if msg.Size > maxMessageSize {
		return fmt.Errorf("%w: %v > %v", errMsgTooLarge, msg.Size, maxMessageSize)
	}
	defer msg.Discard()

	var handlers = aa0

	// Track the amount of time it takes to serve the request and run the handler
	if metrics.Enabled {
		h := fmt.Sprintf("%s/%s/%d/%#02x", p2p.HandleHistName, ProtocolName, peer.Version(), msg.Code)
		defer func(start time.Time) {
			sampler := func() metrics.Sample {
				return metrics.ResettingSample(
					metrics.NewExpDecaySample(1028, 0.015),
				)
			}
			metrics.GetOrRegisterHistogramLazy(h, nil, sampler).Update(time.Since(start).Microseconds())
		}(time.Now())
	}
	if handler, canHandle := handlers[msg.Code]; canHandle {
		return handler(backend, msg, peer)
	}
	return fmt.Errorf("%w: %v", errInvalidMsgCode, msg.Code)
}
