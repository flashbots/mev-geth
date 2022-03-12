package aa

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	// This is the target size for the packs of userops or announcements. A
	// pack can get larger than this if a single userop exceeds this size.
	maxUserOpPacketSize = 100 * 1024
)

// broadcastUserOps is a write loop that schedules userops broadcasts
// to the remote peer. The goal is to have an async writer that does not lock up
// node internals and at the same time rate limits queued data.
func (p *Peer) broadcastUserOps() {
	defer p.wg.Done()
	var (
		queue  []common.Hash         // Queue of hashes to broadcast as full userops
		done   chan struct{}         // Non-nil if background broadcaster is running
		fail   = make(chan error, 1) // Channel used to receive network error
		failed bool                  // Flag whether a send failed, discard everything onward
	)
	for {
		// If there's no in-flight broadcast running, check if a new one is needed
		if done == nil && len(queue) > 0 {
			// Pile transaction until we reach our allowed network limit
			var (
				hashesCount uint64
				ops         []*types.UserOp
				size        common.StorageSize
			)
			for i := 0; i < len(queue) && size < maxUserOpPacketSize; i++ {
				if op := p.opPool.Get(queue[i]); op != nil {
					ops = append(ops, op)
					// size += op.Size()
				}
				hashesCount++
			}
			queue = queue[:copy(queue, queue[hashesCount:])]

			// If there's anything available to transfer, fire up an async writer
			if len(ops) > 0 {
				done = make(chan struct{})
				go func() {
					if err := p.SendUserOps(ops); err != nil {
						fail <- err
						return
					}
					close(done)
					p.Log().Trace("Sent userops", "count", len(ops))
				}()
			}
		}
		// Transfer goroutine may or may not have been started, listen for events
		select {
		case hashes := <-p.opBroadcast:
			// If the connection failed, discard all userop events
			if failed {
				continue
			}
			// New batch of userops to be broadcast, queue them (with cap)
			queue = append(queue, hashes...)
			if len(queue) > maxQueuedOps {
				// Fancy copy and resize to ensure buffer doesn't grow indefinitely
				queue = queue[:copy(queue, queue[len(queue)-maxQueuedOps:])]
			}

		case <-done:
			done = nil

		case <-fail:
			failed = true

		case <-p.term:
			return
		}
	}
}
