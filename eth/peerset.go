// Copyright 2020 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package eth

import (
	"errors"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/eth/protocols/aa"
	"github.com/ethereum/go-ethereum/eth/protocols/eth"
	"github.com/ethereum/go-ethereum/eth/protocols/snap"
	"github.com/ethereum/go-ethereum/p2p"
)

var (
	// errPeerSetClosed is returned if a peer is attempted to be added or removed
	// from the peer set after it has been terminated.
	errPeerSetClosed = errors.New("peerset closed")

	// errPeerAlreadyRegistered is returned if a peer is attempted to be added
	// to the peer set, but one with the same id already exists.
	errPeerAlreadyRegistered = errors.New("peer already registered")

	// errPeerNotRegistered is returned if a peer is attempted to be removed from
	// a peer set, but no peer with the given id exists.
	errPeerNotRegistered = errors.New("peer not registered")

	// errSnapWithoutEth is returned if a peer attempts to connect only on the
	// snap protocol without advertizing the eth main protocol.
	errSnapWithoutEth = errors.New("peer connected on snap without compatible eth support")

	// errAAWithoutEth is returned if a peer attempts to connect only on the
	// aa protocol without advertizing the eth main protocol.
	errAAWithoutEth = errors.New("peer connected on aa without compatible eth support")
)

// peerSet represents the collection of active peers currently participating in
// the `eth` protocol, with or without the `snap` extension.
type peerSet struct {
	peers     map[string]*ethPeer // Peers connected on the `eth` protocol
	aaPeers   map[string]*aaPeer  // Peers connected on the `aa` sub-protocol
	snapPeers int                 // Number of `snap` compatible peers for connection prioritization

	snapWait map[string]chan *snap.Peer // Peers connected on `eth` waiting for their snap extension
	snapPend map[string]*snap.Peer      // Peers connected on the `snap` protocol, but not yet on `eth`

	aaWait map[string]chan *aa.Peer // Peers connected on `eth` waiting for their snap extension
	aaPend map[string]*aa.Peer      // Peers connected on the `aa` protocol, but not yet on `eth`

	lock   sync.RWMutex
	closed bool
}

// newPeerSet creates a new peer set to track the active participants.
func newPeerSet() *peerSet {
	return &peerSet{
		peers:    make(map[string]*ethPeer),
		aaPeers:  make(map[string]*aaPeer),
		snapWait: make(map[string]chan *snap.Peer),
		snapPend: make(map[string]*snap.Peer),
		aaWait:   make(map[string]chan *aa.Peer),
		aaPend:   make(map[string]*aa.Peer),
	}
}

// registerSnapExtension unblocks an already connected `eth` peer waiting for its
// `snap` extension, or if no such peer exists, tracks the extension for the time
// being until the `eth` main protocol starts looking for it.
func (ps *peerSet) registerSnapExtension(peer *snap.Peer) error {
	// Reject the peer if it advertises `snap` without `eth` as `snap` is only a
	// satellite protocol meaningful with the chain selection of `eth`
	if !peer.RunningCap(eth.ProtocolName, eth.ProtocolVersions) {
		return errSnapWithoutEth
	}
	// Ensure nobody can double connect
	ps.lock.Lock()
	defer ps.lock.Unlock()

	id := peer.ID()
	if _, ok := ps.peers[id]; ok {
		return errPeerAlreadyRegistered // avoid connections with the same id as existing ones
	}
	if _, ok := ps.snapPend[id]; ok {
		return errPeerAlreadyRegistered // avoid connections with the same id as pending ones
	}
	// Inject the peer into an `eth` counterpart is available, otherwise save for later
	if wait, ok := ps.snapWait[id]; ok {
		delete(ps.snapWait, id)
		wait <- peer
		return nil
	}
	ps.snapPend[id] = peer
	return nil
}

// waitExtensions blocks until all satellite protocols are connected and tracked
// by the peerset.
func (ps *peerSet) waitSnapExtension(peer *eth.Peer) (*snap.Peer, error) {
	// If the peer does not support a compatible `snap`, don't wait
	if !peer.RunningCap(snap.ProtocolName, snap.ProtocolVersions) {
		return nil, nil
	}
	// Ensure nobody can double connect
	ps.lock.Lock()

	id := peer.ID()
	if _, ok := ps.peers[id]; ok {
		ps.lock.Unlock()
		return nil, errPeerAlreadyRegistered // avoid connections with the same id as existing ones
	}
	if _, ok := ps.snapWait[id]; ok {
		ps.lock.Unlock()
		return nil, errPeerAlreadyRegistered // avoid connections with the same id as pending ones
	}
	// If `snap` already connected, retrieve the peer from the pending set
	if snap, ok := ps.snapPend[id]; ok {
		delete(ps.snapPend, id)

		ps.lock.Unlock()
		return snap, nil
	}
	// Otherwise wait for `snap` to connect concurrently
	wait := make(chan *snap.Peer)
	ps.snapWait[id] = wait
	ps.lock.Unlock()

	return <-wait, nil
}

// registerAAExtension unblocks an already connected `eth` peer waiting for its
// `aa` extension, or if no such peer exists, tracks the extension for the time
// being until the `eth` main protocol starts looking for it.
func (ps *peerSet) registerAAExtension(peer *aa.Peer) error {
	// Reject the peer if it advertises `aa` without `eth` as `aa` is only a
	// satellite protocol of `eth`
	if !peer.RunningCap(eth.ProtocolName, eth.ProtocolVersions) {
		return errAAWithoutEth
	}
	// Ensure nobody can double connect
	ps.lock.Lock()
	defer ps.lock.Unlock()

	id := peer.ID()
	if _, ok := ps.peers[id]; ok {
		return errPeerAlreadyRegistered // avoid connections with the same id as existing ones
	}
	if _, ok := ps.aaPend[id]; ok {
		return errPeerAlreadyRegistered // avoid connections with the same id as pending ones
	}
	// Inject the peer into an `eth` counterpart is available, otherwise save for later
	if wait, ok := ps.aaWait[id]; ok {
		delete(ps.aaWait, id)
		wait <- peer
		return nil
	}
	ps.aaPend[id] = peer
	return nil
}

// waitExtensions blocks until all satellite protocols are connected and tracked
// by the peerset.
func (ps *peerSet) waitAAExtension(peer *eth.Peer) (*aa.Peer, error) {
	// If the peer does not support a compatible `aa`, don't wait
	if !peer.RunningCap(aa.ProtocolName, aa.ProtocolVersions) {
		return nil, nil
	}
	// Ensure nobody can double connect
	ps.lock.Lock()

	id := peer.ID()
	if _, ok := ps.peers[id]; ok {
		ps.lock.Unlock()
		return nil, errPeerAlreadyRegistered // avoid connections with the same id as existing ones
	}
	if _, ok := ps.aaWait[id]; ok {
		ps.lock.Unlock()
		return nil, errPeerAlreadyRegistered // avoid connections with the same id as pending ones
	}
	// If `aa` already connected, retrieve the peer from the pending set
	if aa, ok := ps.aaPend[id]; ok {
		delete(ps.aaPend, id)

		ps.lock.Unlock()
		return aa, nil
	}
	// Otherwise wait for `snap` to connect concurrently
	wait := make(chan *aa.Peer)
	ps.aaWait[id] = wait
	ps.lock.Unlock()

	return <-wait, nil
}

// registerPeer injects a new `eth` peer into the working set, or returns an error
// if the peer is already known.
func (ps *peerSet) registerPeer(peer *eth.Peer, snapExt *snap.Peer, aaExt *aa.Peer) error {
	// Start tracking the new peer
	ps.lock.Lock()
	defer ps.lock.Unlock()

	if ps.closed {
		return errPeerSetClosed
	}
	id := peer.ID()
	if _, ok := ps.peers[id]; ok {
		return errPeerAlreadyRegistered
	}
	eth := &ethPeer{
		Peer: peer,
	}
	if snapExt != nil {
		eth.snapExt = &snapPeer{snapExt}
		ps.snapPeers++
	}
	if aaExt != nil {
		aa := &aaPeer{aaExt}
		eth.aaExt = aa
		ps.aaPeers[id] = aa
	}
	ps.peers[id] = eth
	return nil
}

// unregisterPeer removes a remote peer from the active set, disabling any further
// actions to/from that particular entity.
func (ps *peerSet) unregisterPeer(id string) error {
	ps.lock.Lock()
	defer ps.lock.Unlock()

	peer, ok := ps.peers[id]
	if !ok {
		return errPeerNotRegistered
	}
	delete(ps.peers, id)
	if peer.snapExt != nil {
		ps.snapPeers--
	}
	if peer.aaExt != nil {
		delete(ps.aaPeers, id)
	}
	return nil
}

// peer retrieves the registered peer with the given id.
func (ps *peerSet) peer(id string) *ethPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return ps.peers[id]
}

// peersWithoutBlock retrieves a list of peers that do not have a given block in
// their set of known hashes so it might be propagated to them.
func (ps *peerSet) peersWithoutBlock(hash common.Hash) []*ethPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	list := make([]*ethPeer, 0, len(ps.peers))
	for _, p := range ps.peers {
		if !p.KnownBlock(hash) {
			list = append(list, p)
		}
	}
	return list
}

// peersWithoutTransaction retrieves a list of peers that do not have a given
// transaction in their set of known hashes.
func (ps *peerSet) peersWithoutTransaction(hash common.Hash) []*ethPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	list := make([]*ethPeer, 0, len(ps.peers))
	for _, p := range ps.peers {
		if !p.KnownTransaction(hash) {
			list = append(list, p)
		}
	}
	return list
}

// peersWithoutUserOp retrieves a list of peers that do not have a given
// transaction in their set of known hashes.
func (ps *peerSet) peersWithoutUserOp(reqId common.Hash) []*aaPeer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	list := make([]*aaPeer, 0, len(ps.aaPeers))
	for _, p := range ps.aaPeers {
		if !p.KnownUserOp(reqId) {
			list = append(list, p)
		}
	}
	return list
}

// len returns if the current number of `eth` peers in the set. Since the `snap`
// peers are tied to the existence of an `eth` connection, that will always be a
// subset of `eth`.
func (ps *peerSet) len() int {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return len(ps.peers)
}

// snapLen returns if the current number of `snap` peers in the set.
func (ps *peerSet) snapLen() int {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return ps.snapPeers
}

// aaLen returns if the current number of `aa` peers in the set.
func (ps *peerSet) aaLen() int {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	return len(ps.aaPeers)
}

// peerWithHighestTD retrieves the known peer with the currently highest total
// difficulty.
func (ps *peerSet) peerWithHighestTD() *eth.Peer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()

	var (
		bestPeer *eth.Peer
		bestTd   *big.Int
	)
	for _, p := range ps.peers {
		if _, td := p.Head(); bestPeer == nil || td.Cmp(bestTd) > 0 {
			bestPeer, bestTd = p.Peer, td
		}
	}
	return bestPeer
}

// close disconnects all peers.
func (ps *peerSet) close() {
	ps.lock.Lock()
	defer ps.lock.Unlock()

	for _, p := range ps.peers {
		p.Disconnect(p2p.DiscQuitting)
	}
	ps.closed = true
}
