package aa

import (
	"errors"

	"github.com/ethereum/go-ethereum/core/types"
)

// Constants to match up protocol versions and messages
const (
	AA0 = 0
)

// ProtocolName is the official short name of the `aa` protocol used during
// devp2p capability negotiation.
const ProtocolName = "aa"

// ProtocolVersions are the supported versions of the `aa` protocol (first
// is primary).
var ProtocolVersions = []uint{AA0}

// protocolLengths are the number of implemented message corresponding to
// different protocol versions.
var protocolLengths = map[uint]uint64{AA0: 4}

// maxMessageSize is the maximum cap on the size of a protocol message.
const maxMessageSize = 10 * 1024 * 1024

const (
	UserOpsMsg = 0x00
)

var (
	errMsgTooLarge             = errors.New("message too long")
	errDecode                  = errors.New("invalid message")
	errInvalidMsgCode          = errors.New("invalid message code")
	errProtocolVersionMismatch = errors.New("protocol version mismatch")
)

// Packet represents a p2p message in the `aa` protocol.
type Packet interface {
	Name() string // Name returns a string corresponding to the message type.
	Kind() byte   // Kind returns the message type.
}

// UserOpsPacket is the network packet for batches of userops with entrypoint
type UserOpsPacket []*types.UserOp

func (*UserOpsPacket) Name() string { return "UserOps" }
func (*UserOpsPacket) Kind() byte   { return UserOpsMsg }
