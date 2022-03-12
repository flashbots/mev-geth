package aa

import "fmt"

func handleUserOps(backend Backend, msg Decoder, peer *Peer) error {
	// Ops arrived, make sure we can handle them
	if !backend.AcceptOps() {
		return nil
	}
	var ops UserOpsPacket
	if err := msg.Decode(&ops); err != nil {
		return fmt.Errorf("%w: message %v: %v", errDecode, msg, err)
	}
	peer.Log().Debug("Received UserOpsMsg", "len", len(ops))
	chainId := backend.ChainID()
	// Schedule all the unknown hashes for retrieval
	for _, op := range ops {
		// TODO(jmederos): why not standarize chainId as part of the userop
		op.ChainId = chainId
		reqId, _ := op.RequestId()
		peer.markUserOp(reqId)
	}
	return backend.Handle(peer, &ops)
}
