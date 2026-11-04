package h265

import "encoding/binary"

const naluTypeAP = 48 // RFC 7798 aggregation packet: several NAL units in one RTP packet

// auBuilder puts RTP payloads together into access units (frames) of length-prefixed NAL units. A frame with a part missing
// is thrown away, and so is everything after it until the next keyframe, because those frames refer to what was lost: a
// decoder given them shows corrupted video or stops, where a short freeze is harmless.
type auBuilder struct {
	ps      []byte // parameter sets from the SDP, put before a keyframe
	buf     []byte
	nuStart int
	ts      uint32
	inFU    bool // a fragmented NAL unit is open
	broken  bool // this frame misses a part
	hasKey  bool // this frame is a keyframe
	needKey bool // nothing can be decoded until the next keyframe

	Frames, Dropped int
}

func newAUBuilder(ps []byte) *auBuilder {
	return &auBuilder{ps: ps, buf: make([]byte, 0, 512*1024)}
}

func isIRAP(nuType byte) bool {
	return nuType >= 16 && nuType <= 21
}

func (a *auBuilder) clear() {
	a.buf = a.buf[:0]
	a.inFU = false
	a.broken = false
	a.hasKey = false
}

// lost tells that packets went missing.
func (a *auBuilder) lost() {
	a.clear()
	a.broken = true
	a.needKey = true
}

// add returns a finished frame, valid until the next call, or nil.
func (a *auBuilder) add(data []byte, marker bool, ts uint32) []byte {
	if len(data) < 3 {
		return nil
	}

	if ts != a.ts {
		a.ts = ts
		if a.broken {
			a.clear() // the broken frame is over, even if its marker got lost
		}
	}

	switch nuType := (data[0] >> 1) & 0x3F; nuType {
	case NALUTypeFU:
		a.addFU(data)
	case naluTypeAP:
		a.addAP(data)
	default:
		a.buf = binary.BigEndian.AppendUint32(a.buf, uint32(len(data)))
		a.buf = append(a.buf, data...)
		a.hasKey = a.hasKey || isIRAP(nuType)
	}

	if !marker {
		return nil
	}

	au := a.buf
	damaged := a.broken || a.inFU
	wanted := !damaged && len(au) > 0 && (!a.needKey || a.hasKey)
	if damaged {
		a.needKey = true
	} else if wanted {
		a.needKey = false
	}
	a.clear()

	if !wanted {
		a.Dropped++
		return nil
	}
	a.Frames++
	return au
}

func (a *auBuilder) addFU(data []byte) {
	nuType := data[2] & 0x3F
	switch data[2] >> 6 {
	case 0b10: // start
		if a.inFU {
			a.broken = true // the end of the previous one is missing
		}
		if len(a.buf) == 0 && nuType >= 19 && nuType <= 21 {
			a.buf = append(a.buf, a.ps...)
		}
		a.nuStart = len(a.buf)
		a.buf = append(a.buf, 0, 0, 0, 0, (data[0]&0x81)|(nuType<<1), data[1])
		a.buf = append(a.buf, data[3:]...)
		a.inFU = true
		a.hasKey = a.hasKey || isIRAP(nuType)
	case 0b00: // middle
		if !a.inFU {
			a.broken = true
			return
		}
		a.buf = append(a.buf, data[3:]...)
	case 0b01: // end
		if !a.inFU {
			a.broken = true
			return
		}
		a.buf = append(a.buf, data[3:]...)
		binary.BigEndian.PutUint32(a.buf[a.nuStart:], uint32(len(a.buf)-a.nuStart-4))
		a.inFU = false
	case 0b11: // start and end in one: wrong per RFC 7798, but the OpenIPC project sends it
		a.buf = binary.BigEndian.AppendUint32(a.buf, uint32(len(data))-1)
		a.buf = append(a.buf, (data[0]&0x81)|(nuType<<1), data[1])
		a.buf = append(a.buf, data[3:]...)
		a.hasKey = a.hasKey || isIRAP(nuType)
	}
}

func (a *auBuilder) addAP(data []byte) {
	for i := 2; i < len(data); {
		if i+2 > len(data) {
			a.broken = true
			return
		}
		size := int(binary.BigEndian.Uint16(data[i:]))
		i += 2
		if size < 2 || i+size > len(data) {
			a.broken = true
			return
		}
		a.buf = binary.BigEndian.AppendUint32(a.buf, uint32(size))
		a.buf = append(a.buf, data[i:i+size]...)
		a.hasKey = a.hasKey || isIRAP((data[i]>>1)&0x3F)
		i += size
	}
}
