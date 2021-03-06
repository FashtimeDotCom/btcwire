// Copyright (c) 2013-2014 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package btcwire

import (
	"fmt"
	"io"
	"net"
	"time"
)

// MaxUserAgentLen is the maximum allowed length for the user agent field in a
// version message (MsgVersion).
const MaxUserAgentLen = 2000

// MsgVersion implements the Message interface and represents a bitcoin version
// message.  It is used for a peer to advertise itself as soon as an outbound
// connection is made.  The remote peer then uses this information along with
// its own to negotiate.  The remote peer must then respond with a version
// message of its own containing the negotiated values followed by a verack
// message (MsgVerAck).  This exchange must take place before any further
// communication is allowed to proceed.
type MsgVersion struct {
	// Version of the protocol the node is using.
	ProtocolVersion int32

	// Bitfield which identifies the enabled services.
	Services ServiceFlag

	// Time the message was generated.  This is encoded as an int64 on the wire.
	Timestamp time.Time

	// Address of the remote peer.
	AddrYou NetAddress

	// Address of the local peer.
	AddrMe NetAddress

	// Unique value associated with message that is used to detect self
	// connections.
	Nonce uint64

	// The user agent that generated messsage.  This is a encoded as a varString
	// on the wire.  This has a max length of MaxUserAgentLen.
	UserAgent string

	// Last block seen by the generator of the version message.
	LastBlock int32
}

// HasService returns whether the specified service is supported by the peer
// that generated the message.
func (msg *MsgVersion) HasService(service ServiceFlag) bool {
	if msg.Services&service == service {
		return true
	}
	return false
}

// AddService adds service as a supported service by the peer generating the
// message.
func (msg *MsgVersion) AddService(service ServiceFlag) {
	msg.Services |= service
}

// BtcDecode decodes r using the bitcoin protocol encoding into the receiver.
// This is part of the Message interface implementation.
func (msg *MsgVersion) BtcDecode(r io.Reader, pver uint32) error {
	var sec int64
	err := readElements(r, &msg.ProtocolVersion, &msg.Services, &sec)
	if err != nil {
		return err
	}
	msg.Timestamp = time.Unix(sec, 0)

	err = readNetAddress(r, pver, &msg.AddrYou, false)
	if err != nil {
		return err
	}

	err = readNetAddress(r, pver, &msg.AddrMe, false)
	if err != nil {
		return err
	}

	err = readElement(r, &msg.Nonce)
	if err != nil {
		return err
	}

	userAgent, err := readVarString(r, pver)
	if err != nil {
		return err
	}
	if len(userAgent) > MaxUserAgentLen {
		str := fmt.Sprintf("user agent too long [len %v, max %v]",
			len(userAgent), MaxUserAgentLen)
		return messageError("MsgVersion.BtcDecode", str)
	}
	msg.UserAgent = userAgent

	err = readElement(r, &msg.LastBlock)
	if err != nil {
		return err
	}

	return nil
}

// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgVersion) BtcEncode(w io.Writer, pver uint32) error {
	if len(msg.UserAgent) > MaxUserAgentLen {
		str := fmt.Sprintf("user agent too long [len %v, max %v]",
			len(msg.UserAgent), MaxUserAgentLen)
		return messageError("MsgVersion.BtcEncode", str)
	}

	err := writeElements(w, msg.ProtocolVersion, msg.Services,
		msg.Timestamp.Unix())
	if err != nil {
		return err
	}

	err = writeNetAddress(w, pver, &msg.AddrYou, false)
	if err != nil {
		return err
	}

	err = writeNetAddress(w, pver, &msg.AddrMe, false)
	if err != nil {
		return err
	}

	err = writeElement(w, msg.Nonce)
	if err != nil {
		return err
	}

	err = writeVarString(w, pver, msg.UserAgent)
	if err != nil {
		return err
	}

	err = writeElement(w, msg.LastBlock)
	if err != nil {
		return err
	}

	return nil
}

// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgVersion) Command() string {
	return cmdVersion
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgVersion) MaxPayloadLength(pver uint32) uint32 {
	// XXX: <= 106 different
	// XXX: >= 70001 different

	// Protocol version 4 bytes + services 8 bytes + timestamp 8 bytes + remote
	// and local net addresses + nonce 8 bytes + length of user agent (varInt) +
	// max allowed useragent length + last block 4 bytes.
	return 32 + (maxNetAddressPayload(pver) * 2) + maxVarIntPayload + MaxUserAgentLen
}

// NewMsgVersion returns a new bitcoin version message that conforms to the
// Message interface using the passed parameters and defaults for the remaining
// fields.
func NewMsgVersion(me *NetAddress, you *NetAddress, nonce uint64,
	userAgent string, lastBlock int32) *MsgVersion {

	// Limit the Timestamp to millisecond precision since the protocol
	// doesn't support better.
	return &MsgVersion{
		ProtocolVersion: int32(ProtocolVersion),
		Services:        0,
		Timestamp:       time.Unix(time.Now().Unix(), 0),
		AddrYou:         *you,
		AddrMe:          *me,
		Nonce:           nonce,
		UserAgent:       userAgent,
		LastBlock:       lastBlock,
	}
}

// NewMsgVersionFromConn is a convenience function that extracts the remote
// and local address from conn and returns a new bitcoin version message that
// conforms to the Message interface.  See NewMsgVersion.
func NewMsgVersionFromConn(conn net.Conn, nonce uint64, userAgent string,
	lastBlock int32) (*MsgVersion, error) {

	// Don't assume any services until we know otherwise.
	lna, err := NewNetAddress(conn.LocalAddr(), 0)
	if err != nil {
		return nil, err
	}

	// Don't assume any services until we know otherwise.
	rna, err := NewNetAddress(conn.RemoteAddr(), 0)
	if err != nil {
		return nil, err
	}

	return NewMsgVersion(lna, rna, nonce, userAgent, lastBlock), nil
}
