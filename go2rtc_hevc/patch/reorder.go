package h265

import (
	"os"
	"strconv"
	"time"
)

const (
	// Packets that overtook each other are close together. Cameras that do resend need more: a retransmission arrives after
	// about one round trip plus pion's 100 ms NACK interval. H265_REORDER_WAIT_MS overrides this.
	reorderMaxWait = 100 * time.Millisecond
	reorderMaxHeld = 512 // about a second of a 5 Mbit/s stream
	// Further away than this it is a new stream, not a late or early packet.
	reorderNewStream = 5000
)

func envMillis(name string, standard time.Duration) time.Duration {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return time.Duration(v) * time.Millisecond
	}
	return standard
}

// reorder hands packets on in sequence order. WebRTC runs over UDP: packets can overtake each other, and a retransmitted
// packet always arrives after the ones sent later. Without this, any such packet counts as lost.
type reorder[T any] struct {
	maxHeld int
	maxWait time.Duration
	started bool
	next    uint16
	held    map[uint16]T
	since   time.Time

	Held, Filled, Late, Lost, Gaps, MaxGap, LastGap int
}

func newReorder[T any](maxHeld int, maxWait time.Duration) *reorder[T] {
	return &reorder[T]{maxHeld: maxHeld, maxWait: maxWait, held: map[uint16]T{}}
}

// add gives each packet to emit in order. When the wait for a missing packet is over, it calls lost and goes on after the gap.
func (r *reorder[T]) add(seq uint16, item T, now time.Time, emit func(T), lost func()) {
	if !r.started {
		r.started = true
		r.next = seq
	}

	d := int16(seq - r.next)
	if d > reorderNewStream || d < -reorderNewStream {
		r.held = map[uint16]T{}
		r.next = seq
		d = 0
		r.LastGap = 0
		lost()
	}

	switch {
	case d < 0:
		r.Late++ // a duplicate, or it comes after we gave up on it
	case d == 0:
		if len(r.held) > 0 {
			r.Filled++ // a late packet closed a gap
		}
		emit(item)
		r.next++
		r.flush(now, emit)
	default:
		if _, ok := r.held[seq]; ok {
			return
		}
		if len(r.held) == 0 {
			r.since = now
		}
		r.held[seq] = item
		r.Held++
		if len(r.held) >= r.maxHeld || now.Sub(r.since) >= r.maxWait {
			r.skipGap(now, emit, lost)
		}
	}
}

func (r *reorder[T]) flush(now time.Time, emit func(T)) {
	for {
		item, ok := r.held[r.next]
		if !ok {
			break
		}
		delete(r.held, r.next)
		emit(item)
		r.next++
	}
	if len(r.held) > 0 {
		r.since = now // a further gap gets its own time to fill
	}
}

func (r *reorder[T]) skipGap(now time.Time, emit func(T), lost func()) {
	for gap := 1; gap < 1<<16; gap++ {
		if _, ok := r.held[r.next+uint16(gap)]; ok {
			r.Lost += gap
			r.Gaps++
			r.LastGap = gap
			if gap > r.MaxGap {
				r.MaxGap = gap
			}
			r.next += uint16(gap)
			break
		}
	}
	lost()
	r.flush(now, emit)
}
