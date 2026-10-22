package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// hub holds what the HTTP side serves: the latest state, the event ring,
// and the open streams. The collector publishes once per round; requests
// only read. One lock covers all three, so a stream that connects gets its
// replay and then every later event, with nothing lost in between.
type hub struct {
	mu        sync.Mutex
	epoch     string
	state     []byte // the latest state as JSON; nil before the first round
	stateSSE  []byte // the same, framed for the stream
	events    ring
	clients   map[chan []byte]struct{}
	maxClient int
	queue     int
	closed    bool
	// history is set by the collector before serving; read-only after.
	history *history
}

func newHub(epoch string) *hub {
	return &hub{
		epoch:     epoch,
		events:    ring{size: 2000},
		clients:   map[chan []byte]struct{}{},
		maxClient: 64,
		queue:     32,
	}
}

// publish stores one round and its events and sends them to every stream.
// A stream whose queue is full is dropped, never waited for: the collector
// must not stall on a slow browser.
func (h *hub) publish(s state, evs []event) []event {
	stateJSON, err := json.Marshal(s)
	if err != nil {
		panic(err) // a state always marshals: no channels, funcs or NaN
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state, h.stateSSE = stateJSON, frame("state", "", stateJSON)
	msgs := [][]byte{h.stateSSE}
	for i := range evs {
		evs[i].Epoch = h.epoch
		evs[i] = h.events.add(evs[i])
		msgs = append(msgs, h.eventSSE(evs[i]))
	}
	for c := range h.clients {
		for _, m := range msgs {
			select {
			case c <- m:
				continue
			default:
			}
			h.drop(c)
			break
		}
	}
	return evs
}

func (h *hub) eventSSE(e event) []byte {
	b, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return frame("event", fmt.Sprintf("%s/%d", h.epoch, e.ID), b)
}

func frame(name, id string, data []byte) []byte {
	var b strings.Builder
	if id != "" {
		b.WriteString("id: " + id + "\n")
	}
	b.WriteString("event: " + name + "\ndata: ")
	b.Write(data)
	b.WriteString("\n\n")
	return []byte(b.String())
}

// subscribe opens a stream. It returns what to write first (retry, the
// state, then the replay or a gap) and the channel for the rest, or false
// when the cap of streams is reached or the hub is closed.
func (h *hub) subscribe(lastEventID string) ([]byte, chan []byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.clients) >= h.maxClient {
		return nil, nil, false
	}
	first := []byte("retry: 2000\n\n")
	first = append(first, h.stateSSE...)
	if lastEventID != "" {
		epoch, idText, _ := strings.Cut(lastEventID, "/")
		id, err := strconv.ParseUint(idText, 10, 64)
		reason := ""
		var evs []event
		switch {
		case err != nil || epoch != h.epoch:
			reason = "console_restarted"
		default:
			var complete bool
			if evs, complete = h.events.after(id, h.events.size); !complete {
				reason = "evicted"
			}
		}
		if reason != "" {
			gap, _ := json.Marshal(map[string]any{"schema_version": 1, "epoch": h.epoch, "reason": reason, "since_id": id})
			first = append(first, frame("gap", "", gap)...)
		}
		for _, e := range evs {
			first = append(first, h.eventSSE(e)...)
		}
	}
	c := make(chan []byte, h.queue)
	h.clients[c] = struct{}{}
	return first, c, true
}

// unsubscribe is for a stream that ends on its own; drop closes the channel
// so the stream's handler returns.
func (h *hub) unsubscribe(c chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		h.drop(c)
	}
}

func (h *hub) drop(c chan []byte) {
	delete(h.clients, c)
	close(c)
}

// close ends every stream and refuses new ones: for shutdown.
func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for c := range h.clients {
		h.drop(c)
	}
}

func (h *hub) latest() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *hub) after(id uint64, limit int) []event {
	h.mu.Lock()
	defer h.mu.Unlock()
	evs, _ := h.events.after(id, limit)
	return evs
}
