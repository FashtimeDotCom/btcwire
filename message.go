// Copyright (c) 2013-2014 Conformal Systems LLC.
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package btcwire

import (
	"bytes"
	"fmt"
	"io"
	"unicode/utf8"
)

// commandSize is the fixed size of all commands in the common bitcoin message
// header.  Shorter commands must be zero padded.
const commandSize = 12

// maxMessagePayload is the maximum bytes a message can be regardless of other
// individual limits imposed by messages themselves.
const maxMessagePayload = (1024 * 1024 * 32) // 32MB

// Commands used in bitcoin message headers which describe the type of message.
const (
	cmdVersion    = "version"
	cmdVerAck     = "verack"
	cmdGetAddr    = "getaddr"
	cmdAddr       = "addr"
	cmdGetBlocks  = "getblocks"
	cmdInv        = "inv"
	cmdGetData    = "getdata"
	cmdNotFound   = "notfound"
	cmdBlock      = "block"
	cmdTx         = "tx"
	cmdGetHeaders = "getheaders"
	cmdHeaders    = "headers"
	cmdPing       = "ping"
	cmdPong       = "pong"
	cmdAlert      = "alert"
	cmdMemPool    = "mempool"
)

// Message is an interface that describes a bitcoin message.  A type that
// implements Message has complete control over the representation of its data
// and may therefore contain additional or fewer fields than those which
// are used directly in the protocol encoded message.
type Message interface {
	BtcDecode(io.Reader, uint32) error
	BtcEncode(io.Writer, uint32) error
	Command() string
	MaxPayloadLength(uint32) uint32
}

// makeEmptyMessage creates a message of the appropriate concrete type based
// on the command.
func makeEmptyMessage(command string) (Message, error) {
	var msg Message
	switch command {
	case cmdVersion:
		msg = &MsgVersion{}

	case cmdVerAck:
		msg = &MsgVerAck{}

	case cmdGetAddr:
		msg = &MsgGetAddr{}

	case cmdAddr:
		msg = &MsgAddr{}

	case cmdGetBlocks:
		msg = &MsgGetBlocks{}

	case cmdBlock:
		msg = &MsgBlock{}

	case cmdInv:
		msg = &MsgInv{}

	case cmdGetData:
		msg = &MsgGetData{}

	case cmdNotFound:
		msg = &MsgNotFound{}

	case cmdTx:
		msg = &MsgTx{}

	case cmdPing:
		msg = &MsgPing{}

	case cmdPong:
		msg = &MsgPong{}

	case cmdGetHeaders:
		msg = &MsgGetHeaders{}

	case cmdHeaders:
		msg = &MsgHeaders{}

	case cmdAlert:
		msg = &MsgAlert{}

	case cmdMemPool:
		msg = &MsgMemPool{}

	default:
		return nil, fmt.Errorf("unhandled command [%s]", command)
	}
	return msg, nil
}

// messageHeader defines the header structure for all bitcoin protocol messages.
type messageHeader struct {
	magic    BitcoinNet // 4 bytes
	command  string     // 12 bytes
	length   uint32     // 4 bytes
	checksum [4]byte    // 4 bytes
}

// readMessageHeader reads a bitcoin message header from r.
func readMessageHeader(r io.Reader) (*messageHeader, error) {
	var command [commandSize]byte

	hdr := messageHeader{}
	err := readElements(r, &hdr.magic, &command, &hdr.length, &hdr.checksum)
	if err != nil {
		return nil, err
	}

	// Strip trailing zeros from command string.
	hdr.command = string(bytes.TrimRight(command[:], string(0)))

	return &hdr, nil
}

// discardInput reads n bytes from reader r in chunks and discards the read
// bytes.  This is used to skip payloads when various errors occur and helps
// prevent rogue nodes from causing massive memory allocation through forging
// header length.
func discardInput(r io.Reader, n uint32) {
	maxSize := uint32(10 * 1024) // 10k at a time
	numReads := n / maxSize
	bytesRemaining := n % maxSize
	if n > 0 {
		buf := make([]byte, maxSize)
		for i := uint32(0); i < numReads; i++ {
			io.ReadFull(r, buf)
		}
	}
	if bytesRemaining > 0 {
		buf := make([]byte, bytesRemaining)
		io.ReadFull(r, buf)
	}
}

// WriteMessage writes a bitcoin Message to w including the necessary header
// information.
func WriteMessage(w io.Writer, msg Message, pver uint32, btcnet BitcoinNet) error {
	var command [commandSize]byte

	// Enforce max command size.
	cmd := msg.Command()
	if len(cmd) > commandSize {
		str := fmt.Sprintf("command [%s] is too long [max %v]",
			cmd, commandSize)
		return messageError("WriteMessage", str)
	}
	copy(command[:], []byte(cmd))

	// Encode the message payload.
	var bw bytes.Buffer
	err := msg.BtcEncode(&bw, pver)
	if err != nil {
		return err
	}
	payload := bw.Bytes()
	lenp := len(payload)

	// Enforce maximum overall message payload.
	if lenp > maxMessagePayload {
		str := fmt.Sprintf("message payload is too large - encoded "+
			"%d bytes, but maximum message payload is %d bytes",
			lenp, maxMessagePayload)
		return messageError("WriteMessage", str)
	}

	// Enforce maximum message payload based on the message type.
	mpl := msg.MaxPayloadLength(pver)
	if uint32(lenp) > mpl {
		str := fmt.Sprintf("message payload is too large - encoded "+
			"%d bytes, but maximum message payload size for "+
			"messages of type [%s] is %d.", lenp, cmd, mpl)
		return messageError("WriteMessage", str)
	}

	// Create header for the message.
	hdr := messageHeader{}
	hdr.magic = btcnet
	hdr.command = cmd
	hdr.length = uint32(lenp)
	copy(hdr.checksum[:], DoubleSha256(payload)[0:4])

	// Write header.
	err = writeElements(w, hdr.magic, command, hdr.length, hdr.checksum)
	if err != nil {
		return err
	}

	// Write payload.
	_, err = w.Write(payload)
	if err != nil {
		return err
	}
	return nil
}

// ReadMessage reads, validates, and parses the next bitcoin Message from r for
// the provided protocol version and bitcoin network.
func ReadMessage(r io.Reader, pver uint32, btcnet BitcoinNet) (Message, []byte, error) {
	hdr, err := readMessageHeader(r)
	if err != nil {
		return nil, nil, err
	}

	// Enforce maximum message payload.
	if hdr.length > maxMessagePayload {
		str := fmt.Sprintf("message payload is too large - header "+
			"indicates %d bytes, but max message payload is %d "+
			"bytes.", hdr.length, maxMessagePayload)
		return nil, nil, messageError("ReadMessage", str)

	}

	// Check for messages from the wrong bitcoin network.
	if hdr.magic != btcnet {
		discardInput(r, hdr.length)
		str := fmt.Sprintf("message from other network [%v]", hdr.magic)
		return nil, nil, messageError("ReadMessage", str)
	}

	// Check for malformed commands.
	command := hdr.command
	if !utf8.ValidString(command) {
		discardInput(r, hdr.length)
		str := fmt.Sprintf("invalid command %v", []byte(command))
		return nil, nil, messageError("ReadMessage", str)
	}

	// Create struct of appropriate message type based on the command.
	msg, err := makeEmptyMessage(command)
	if err != nil {
		discardInput(r, hdr.length)
		return nil, nil, messageError("ReadMessage", err.Error())
	}

	// Check for maximum length based on the message type as a malicious client
	// could otherwise create a well-formed header and set the length to max
	// numbers in order to exhaust the machine's memory.
	mpl := msg.MaxPayloadLength(pver)
	if hdr.length > mpl {
		discardInput(r, hdr.length)
		str := fmt.Sprintf("payload exceeds max length - header "+
			"indicates %v bytes, but max payload size for "+
			"messages of type [%v] is %v.", hdr.length, command, mpl)
		return nil, nil, messageError("ReadMessage", str)
	}

	// Read payload.
	payload := make([]byte, hdr.length)
	_, err = io.ReadFull(r, payload)
	if err != nil {
		return nil, nil, err
	}

	// Test checksum.
	checksum := DoubleSha256(payload)[0:4]
	if !bytes.Equal(checksum[:], hdr.checksum[:]) {
		str := fmt.Sprintf("payload checksum failed - header "+
			"indicates %v, but actual checksum is %v.",
			hdr.checksum, checksum)
		return nil, nil, messageError("ReadMessage", str)
	}

	// Unmarshal message.
	pr := bytes.NewBuffer(payload)
	err = msg.BtcDecode(pr, pver)
	if err != nil {
		return nil, nil, err
	}

	return msg, payload, nil
}
