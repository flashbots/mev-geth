package aa

import (
	"sync"

	mapset "github.com/deckarep/golang-set"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/p2p"
)

const (
	// maxKnownOps is the maximum userops hashes to keep in the known list
	// before starting to randomly evict them.
	maxKnownOps = 32768

	// maxQueuedOps is the maximum number of userops to queue up before dropping
	// older broadcasts.
	maxQueuedOps = 4096
)

// max is a helper function which returns the larger of the two given integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Peer is a collection of relevant information we have about a `aa` peer.
type Peer struct {
	id string // Unique ID for the peer, cached

	*p2p.Peer                   // The embedded P2P package peer
	rw        p2p.MsgReadWriter // Input/output streams for snap
	version   uint              // Protocol version negotiated

	opPool      UserOpPool         // UserOp pool used by the broadcasters for liveness checks
	knownOps    *knownCache        // Set of userop hashes known to be known by this peer
	opBroadcast chan []common.Hash // Channel used to queue userop propagation requests

	term chan struct{} // Termination channel to stop the broadcasters
	wg   sync.WaitGroup
}

// newPeer create a wrapper for a network connection and negotiated  protocol
// version.
func newPeer(version uint, p *p2p.Peer, rw p2p.MsgReadWriter, opPool UserOpPool) *Peer {
	peer := &Peer{
		id: p.ID().String(),

		Peer:    p,
		rw:      rw,
		version: version,

		opPool:      opPool,
		knownOps:    newKnownCache(maxKnownOps),
		opBroadcast: make(chan []common.Hash),

		term: make(chan struct{}),
	}

	// Start the userop broadcasting loop
	peer.wg.Add(1)
	go peer.broadcastUserOps()

	return peer
}

// Close signals the broadcast goroutine to terminate.
func (p *Peer) Close() {
	p.Log().Debug("Closing peer")
	close(p.term)
	p.wg.Wait()
}

// ID retrieves the peer's unique identifier.
func (p *Peer) ID() string {
	return p.id
}

// Version retrieves the peer's negoatiated `eth` protocol version.
func (p *Peer) Version() uint {
	return p.version
}

// KnownUserOp returns whether peer is known to already have a userop.
func (p *Peer) KnownUserOp(hash common.Hash) bool {
	return p.knownOps.Contains(hash)
}

// markUserOp marks a userop as known for the peer, ensuring that it
// will never be propagated to this particular peer.
func (p *Peer) markUserOp(hash common.Hash) {
	// If we reached the memory allowance, drop a previously known reqid hash
	p.knownOps.Add(hash)
}

// SendUserOps sends userops to the peer and includes the reqids
// in its userop reqids set for future reference.
//
// This method is a helper used by the async userop sender. Don't call it
// directly as the queueing (memory) and transmission (bandwidth) costs should
// not be managed directly.
//
// The reasons this is public is to allow packages using this protocol to write
// tests that directly send messages without having to do the asyn queueing.
func (p *Peer) SendUserOps(ops []*types.UserOp) error {
	// Mark all the userops as known, but ensure we don't overflow our limits
	for _, op := range ops {
		reqId, _ := op.RequestId()
		p.knownOps.Add(reqId)
	}
	return p2p.Send(p.rw, UserOpsMsg, ops)
}

// AsyncSendUserOps queues a list of userops (by reqid) to eventually
// propagate to a remote peer. The number of pending sends are capped (new ones
// will force old sends to be dropped)
func (p *Peer) AsyncSendUserOps(hashes []common.Hash) {
	select {
	case p.opBroadcast <- hashes:
		// Mark all the userops as known, but ensure we don't overflow our limits
		p.knownOps.Add(hashes...)
	case <-p.term:
		p.Log().Debug("Dropping userop propagation", "count", len(hashes))
		return
	}
}

// TODO(jmederos): Try to reuse eth.knownCache
//
// knownCache is a cache for known hashes.
type knownCache struct {
	hashes mapset.Set
	max    int
}

// newKnownCache creates a new knownCache with a max capacity.
func newKnownCache(max int) *knownCache {
	return &knownCache{
		max:    max,
		hashes: mapset.NewSet(),
	}
}

// Add adds a list of elements to the set.
func (k *knownCache) Add(hashes ...common.Hash) {
	for k.hashes.Cardinality() > max(0, k.max-len(hashes)) {
		k.hashes.Pop()
	}
	for _, hash := range hashes {
		k.hashes.Add(hash)
	}
}

// Contains returns whether the given item is in the set.
func (k *knownCache) Contains(hash common.Hash) bool {
	return k.hashes.Contains(hash)
}

// Cardinality returns the number of elements in the set.
func (k *knownCache) Cardinality() int {
	return k.hashes.Cardinality()
}
