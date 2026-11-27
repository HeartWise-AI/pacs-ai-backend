package dicom

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

// Part10UIDs are the UIDs read from the header of a DICOM Part 10 file.
type Part10UIDs struct {
	SOPInstanceUID   string
	StudyInstanceUID string
}

const (
	tagTransferSyntaxUID    uint32 = 0x00020010
	tagSOPInstanceUID       uint32 = 0x00080018
	tagStudyInstanceUID     uint32 = 0x0020000D
	tagItem                 uint32 = 0xFFFEE000
	tagItemDelimitation     uint32 = 0xFFFEE00D
	tagSequenceDelimitation uint32 = 0xFFFEE0DD
	undefinedLength         uint32 = 0xFFFFFFFF
	maxUIDValueLength       uint32 = 66
	maxSequenceDepth               = 32
)

var errMalformedPart10 = errors.New("malformed DICOM part 10 header")

// ReadPart10UIDs reads the SOP Instance UID and the Study Instance UID of a
// DICOM Part 10 file. It stops at the Study Instance UID, discards the values
// of the elements before it without holding them, and fails on elements out
// of order and on deflated or big endian transfer syntaxes.
func ReadPart10UIDs(r io.Reader) (Part10UIDs, error) {
	in := bufio.NewReader(r)
	var uids Part10UIDs

	var preamble [132]byte
	if _, err := io.ReadFull(in, preamble[:]); err != nil || string(preamble[128:]) != "DICM" {
		return uids, errMalformedPart10
	}

	transferSyntax := ""
	for {
		group, err := in.Peek(2)
		if err != nil {
			return uids, errMalformedPart10
		}
		if binary.LittleEndian.Uint16(group) != 0x0002 {
			break
		}
		element, err := readElementHeader(in, false)
		if err != nil || element.length == undefinedLength {
			return uids, errMalformedPart10
		}
		if element.tag == tagTransferSyntaxUID {
			if transferSyntax, err = readUID(in, element.length); err != nil {
				return uids, err
			}
			continue
		}
		if err := discard(in, element.length); err != nil {
			return uids, err
		}
	}

	implicit := false
	switch transferSyntax {
	case "":
		return uids, errMalformedPart10
	case "1.2.840.10008.1.2":
		implicit = true
	case "1.2.840.10008.1.2.1.99", "1.2.840.10008.1.2.2":
		return uids, errors.New("unsupported DICOM transfer syntax")
	}

	previous := uint32(0)
	for {
		element, err := readElementHeader(in, implicit)
		if err != nil || element.tag <= previous || element.tag > tagStudyInstanceUID {
			return uids, errMalformedPart10
		}
		previous = element.tag

		switch element.tag {
		case tagSOPInstanceUID:
			if uids.SOPInstanceUID, err = readUID(in, element.length); err != nil {
				return uids, err
			}
		case tagStudyInstanceUID:
			if uids.StudyInstanceUID, err = readUID(in, element.length); err != nil {
				return uids, err
			}
			return uids, nil
		default:
			if err := skipValue(in, element, implicit, 0); err != nil {
				return uids, err
			}
		}
	}
}

type elementHeader struct {
	tag    uint32
	vr     string
	length uint32
}

func readElementHeader(in io.Reader, implicit bool) (elementHeader, error) {
	var head [8]byte
	if _, err := io.ReadFull(in, head[:]); err != nil {
		return elementHeader{}, err
	}
	element := elementHeader{
		tag: uint32(binary.LittleEndian.Uint16(head[0:2]))<<16 | uint32(binary.LittleEndian.Uint16(head[2:4])),
	}
	// Item and delimitation tags carry no VR in any transfer syntax.
	if implicit || element.tag>>16 == 0xFFFE {
		element.length = binary.LittleEndian.Uint32(head[4:8])
		return element, nil
	}

	element.vr = string(head[4:6])
	switch element.vr {
	case "OB", "OD", "OF", "OL", "OV", "OW", "SQ", "SV", "UC", "UN", "UR", "UT", "UV":
		var long [4]byte
		if _, err := io.ReadFull(in, long[:]); err != nil {
			return elementHeader{}, err
		}
		element.length = binary.LittleEndian.Uint32(long[:])
		if element.length == undefinedLength && element.vr != "SQ" && element.vr != "UN" {
			return elementHeader{}, errMalformedPart10
		}
	default:
		element.length = uint32(binary.LittleEndian.Uint16(head[6:8]))
	}
	return element, nil
}

func readUID(in io.Reader, length uint32) (string, error) {
	if length > maxUIDValueLength {
		return "", errMalformedPart10
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(in, value); err != nil {
		return "", errMalformedPart10
	}
	return strings.TrimRight(string(value), " \x00"), nil
}

func discard(in io.Reader, length uint32) error {
	if _, err := io.CopyN(io.Discard, in, int64(length)); err != nil {
		return errMalformedPart10
	}
	return nil
}

// skipValue skips an element value. A value of undefined length is a
// sequence: its items are walked up to the sequence delimitation item. The
// items of a UN sequence are encoded in implicit VR (PS3.5 6.2.2).
func skipValue(in io.Reader, element elementHeader, implicit bool, depth int) error {
	if element.length != undefinedLength {
		return discard(in, element.length)
	}
	if depth >= maxSequenceDepth {
		return errMalformedPart10
	}
	itemsImplicit := implicit || element.vr == "UN"

	for {
		item, err := readItemHeader(in)
		if err != nil {
			return err
		}
		if item.tag == tagSequenceDelimitation {
			return nil
		}
		if item.tag != tagItem {
			return errMalformedPart10
		}
		if item.length != undefinedLength {
			if err := discard(in, item.length); err != nil {
				return err
			}
			continue
		}
		for {
			nested, err := readElementHeader(in, itemsImplicit)
			if err != nil {
				return errMalformedPart10
			}
			if nested.tag == tagItemDelimitation {
				break
			}
			if nested.tag>>16 == 0xFFFE {
				return errMalformedPart10
			}
			if err := skipValue(in, nested, itemsImplicit, depth+1); err != nil {
				return err
			}
		}
	}
}

func readItemHeader(in io.Reader) (elementHeader, error) {
	item, err := readElementHeader(in, true)
	if err != nil || item.tag>>16 != 0xFFFE {
		return elementHeader{}, errMalformedPart10
	}
	return item, nil
}
