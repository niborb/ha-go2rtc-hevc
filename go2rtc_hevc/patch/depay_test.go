package h265

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/pion/rtp"
)

const (
	typeP   = 1
	typeIDR = 19
)

// One frame in RTP packets: a fragmented NAL unit of the given type, each packet holding its own byte value.
func frame(seq uint16, ts uint32, nuType byte, parts int) []*rtp.Packet {
	var out []*rtp.Packet
	for i := 0; i < parts; i++ {
		fu := nuType
		if i == 0 {
			fu |= 0b10 << 6
		}
		if i == parts-1 {
			fu |= 0b01 << 6
		}
		out = append(out, &rtp.Packet{
			Header:  rtp.Header{SequenceNumber: seq + uint16(i), Timestamp: ts, Marker: i == parts-1},
			Payload: []byte{NALUTypeFU << 1, 1, fu, byte(i + 1), byte(i + 1)},
		})
	}
	return out
}

func run(packets []*rtp.Packet) (frames [][]byte) {
	depay := RTPDepay(&core.Codec{}, func(p *rtp.Packet) {
		frames = append(frames, append([]byte(nil), p.Payload...))
	})
	for _, p := range packets {
		depay(p)
	}
	return
}

func firstType(au []byte) byte {
	return (au[4] >> 1) & 0x3F
}

func TestFrameInOrder(t *testing.T) {
	got := run(frame(10, 1000, typeIDR, 3))
	if len(got) != 1 {
		t.Fatalf("frames: %d", len(got))
	}
	want := []byte{0, 0, 0, 8, typeIDR << 1, 1, 1, 1, 2, 2, 3, 3}
	if !bytes.Equal(got[0], want) {
		t.Fatalf("got %v want %v", got[0], want)
	}
}

func TestLatePacketIsPutBackInPlace(t *testing.T) {
	p := frame(10, 1000, typeIDR, 4)
	got := run([]*rtp.Packet{p[0], p[2], p[3], p[1]}) // the second comes in last, like a retransmission
	want := run(frame(10, 1000, typeIDR, 4))
	if len(got) != 1 || !bytes.Equal(got[0], want[0]) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSequenceWraparound(t *testing.T) {
	p := frame(65534, 1000, typeIDR, 4)
	got := run([]*rtp.Packet{p[0], p[1], p[3], p[2]})
	if len(got) != 1 || got[0][3] != 10 {
		t.Fatalf("got %v", got)
	}
}

func TestLossDropsUntilNextKeyframe(t *testing.T) {
	var frames [][]byte
	now := time.Unix(100, 0)
	r := newReorder[*rtp.Packet](reorderMaxHeld, reorderMaxWait)
	a := newAUBuilder(nil)
	emit := func(p *rtp.Packet) {
		if au := a.add(p.Payload, p.Marker, p.Timestamp); au != nil {
			frames = append(frames, append([]byte(nil), au...))
		}
	}
	send := func(ps []*rtp.Packet, skip int) {
		for i, p := range ps {
			if i == skip {
				continue
			}
			now = now.Add(100 * time.Millisecond) // long enough to give up on a gap after a few packets
			r.add(p.SequenceNumber, p, now, emit, a.lost)
		}
	}

	send(frame(1, 1000, typeIDR, 2), -1) // key: shown
	send(frame(3, 2000, typeP, 2), -1)   // shown
	send(frame(5, 3000, typeP, 3), 1)    // part 2 of 3 never arrives
	send(frame(8, 4000, typeP, 2), -1)   // refers to the lost frame
	send(frame(10, 5000, typeP, 2), -1)  // same
	send(frame(12, 6000, typeIDR, 2), -1)
	send(frame(14, 7000, typeP, 2), -1)

	var types []byte
	for _, au := range frames {
		types = append(types, firstType(au))
	}
	want := []byte{typeIDR, typeP, typeIDR, typeP}
	if !bytes.Equal(types, want) {
		t.Fatalf("frames %v want %v", types, want)
	}
	if r.Lost != 1 {
		t.Fatalf("lost %d", r.Lost)
	}
	if a.Dropped == 0 {
		t.Fatal("nothing counted as dropped")
	}
}

func TestLostMarkerDoesNotLoseTheNextFrame(t *testing.T) {
	a := newAUBuilder(nil)
	first := frame(1, 1000, typeP, 2)
	a.add(first[0].Payload, false, 1000)
	a.lost() // the rest, with the marker, is gone
	var out []byte
	for _, p := range frame(5, 2000, typeIDR, 2) {
		if au := a.add(p.Payload, p.Marker, p.Timestamp); au != nil {
			out = au
		}
	}
	if out == nil || firstType(out) != typeIDR {
		t.Fatalf("keyframe after a lost marker: %v", out)
	}
}

func TestLateAfterGivingUpIsIgnored(t *testing.T) {
	now := time.Unix(100, 0)
	r := newReorder[int](reorderMaxHeld, reorderMaxWait)
	var got []int
	lost := 0
	emit := func(v int) { got = append(got, v) }
	onLost := func() { lost++ }
	r.add(1, 1, now, emit, onLost)
	r.add(3, 3, now, emit, onLost)
	r.add(4, 4, now.Add(reorderMaxWait), emit, onLost) // waited long enough for 2
	r.add(2, 2, now.Add(reorderMaxWait), emit, onLost) // too late
	r.add(5, 5, now.Add(reorderMaxWait), emit, onLost)
	if len(got) != 4 || got[1] != 3 || got[3] != 5 || lost != 1 || r.Late != 1 {
		t.Fatalf("got %v lost %d late %d", got, lost, r.Late)
	}
}

func TestNewStreamStartsOver(t *testing.T) {
	now := time.Unix(100, 0)
	r := newReorder[int](reorderMaxHeld, reorderMaxWait)
	var got []int
	lost := 0
	emit := func(v int) { got = append(got, v) }
	onLost := func() { lost++ }
	r.add(100, 100, now, emit, onLost)
	r.add(30000, 30000, now, emit, onLost)
	r.add(30001, 30001, now, emit, onLost)
	if len(got) != 3 || lost != 1 {
		t.Fatalf("got %v lost %d", got, lost)
	}
}

func TestHoldsNoMoreThanTheLimit(t *testing.T) {
	now := time.Unix(100, 0)
	r := newReorder[int](4, time.Hour)
	var got []int
	lost := 0
	r.add(1, 1, now, func(v int) { got = append(got, v) }, func() { lost++ })
	for s := 3; s <= 6; s++ {
		r.add(uint16(s), s, now, func(v int) { got = append(got, v) }, func() { lost++ })
	}
	if len(got) != 5 || lost != 1 || r.Lost != 1 {
		t.Fatalf("got %v lost %d", got, lost)
	}
}

func TestAggregationPacket(t *testing.T) {
	a := newAUBuilder(nil)
	data := []byte{naluTypeAP << 1, 1}
	for _, n := range [][]byte{{NALUTypeVPS << 1, 1, 9}, {NALUTypeSPS << 1, 1, 8, 8}, {typeIDR << 1, 1, 7}} {
		data = binary.BigEndian.AppendUint16(data, uint16(len(n)))
		data = append(data, n...)
	}
	au := a.add(data, true, 1)
	want := []byte{0, 0, 0, 3, NALUTypeVPS << 1, 1, 9, 0, 0, 0, 4, NALUTypeSPS << 1, 1, 8, 8, 0, 0, 0, 3, typeIDR << 1, 1, 7}
	if !bytes.Equal(au, want) {
		t.Fatalf("got %v", au)
	}

	a = newAUBuilder(nil)
	if au := a.add([]byte{naluTypeAP << 1, 1, 0, 200, 1, 2}, true, 1); au != nil {
		t.Fatalf("truncated aggregation packet gave %v", au)
	}
}
