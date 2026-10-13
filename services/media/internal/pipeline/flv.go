package pipeline

import (
	"encoding/binary"
	"io"
)

// FLV tag types.
const (
	TagAudio  byte = 8
	TagVideo  byte = 9
	TagScript byte = 18
)

const (
	_flvFlagAudio = 0x04
	_flvFlagVideo = 0x01
	_tagHeaderLen = 11
)

// writeFLVHeader writes the 9-byte FLV header plus PreviousTagSize0.
func writeFLVHeader(w io.Writer, hasAudio bool) error {
	flags := byte(_flvFlagVideo)
	if hasAudio {
		flags |= _flvFlagAudio
	}

	_, err := w.Write([]byte{'F', 'L', 'V', 1, flags, 0, 0, 0, 9, 0, 0, 0, 0})

	return err
}

// writeFLVTag writes one tag exactly as received over RTMP (the RTMP message body is the FLV tag body).
func writeFLVTag(w io.Writer, tagType byte, timestamp uint32, body []byte) error {
	var hdr [_tagHeaderLen]byte

	size := len(body)
	hdr[0] = tagType
	// FLV stores 24-bit big-endian size and timestamp, plus the timestamp's top byte: truncation is the format.
	hdr[1], hdr[2], hdr[3] = byte(size>>16), byte(size>>8), byte(size)                //nolint:gosec,mnd // see above
	hdr[4], hdr[5], hdr[6] = byte(timestamp>>16), byte(timestamp>>8), byte(timestamp) //nolint:gosec,mnd // see above
	hdr[7] = byte(timestamp >> 24)                                                    //nolint:mnd // TimestampExtended
	// hdr[8:11] StreamID, always 0

	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}

	if _, err := w.Write(body); err != nil {
		return err
	}

	var prev [4]byte
	binary.BigEndian.PutUint32(prev[:], uint32(_tagHeaderLen+size)) //nolint:gosec // RTMP messages are far below 4 GiB

	_, err := w.Write(prev[:])

	return err
}
