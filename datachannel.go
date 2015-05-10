package webrtc

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/gopherjs/gopherjs/js"
)

type DataChannel struct {
	dc *js.Object

	open  chan struct{}
	recv  chan string
	close chan struct{}
}

func (c Conn) CreateDataChannel(label string, options ...DataChannelOption) DataChannel {
	init := js.M{}
	for _, opt := range options {
		opt.apply(init)
	}
	dc := c.pc.Call("createDataChannel", label, init)
	open := make(chan struct{})
	recv := make(chan string, 10)
	clos := make(chan struct{})
	dc.Set("onopen", func() {
		close(open)
	})
	dc.Set("onmessage", func(e *js.Object) {
		recv <- e.Get("data").String()
	})
	dc.Set("onclose", func() {
		defer catchCloseOfClosedChannel()
		close(clos)
	})
	return DataChannel{dc, open, recv, clos}
}

func (d DataChannel) SendString(s string) (err error) {
	if s == "" {
		panic("cannot send empty string (WebRTC bug?)")
	}
	defer func() {
		x := recover()
		if x == nil {
			return
		}
		if jsErr, ok := x.(*js.Error); ok && jsErr != nil {
			err = jsErr
		} else {
			panic(x)
		}
	}()

	select {
	case <-d.open:
	case <-d.close:
		return fmt.Errorf("DataChannel is closed")
	}
	d.dc.Call("send", s)
	return
}

func (d DataChannel) Recv() (string, error) {
	// Prioritize recv over close.
	select {
	case s := <-d.recv:
		return s, nil
	default:
	}

	select {
	case s := <-d.recv:
		return s, nil
	case <-d.close:
		return "", fmt.Errorf("DataChannel is closed")
	}
}

func (d DataChannel) Close() {
	d.dc.Call("close")

	// A DataChannel that is Closed before onopen fires will not have its onclose callback called.
	// If we don't close(d.close), Send and Recv will hang forever.
	defer catchCloseOfClosedChannel()
	close(d.close)
}

func catchCloseOfClosedChannel() {
	if x := recover(); x != nil {
		if err, ok := x.(runtime.Error); !ok || !strings.Contains(err.Error(), "close of closed channel") {
			panic(x)
		}
	}
}

type DataChannelOption struct {
	apply func(js.M)
}

var Unordered = DataChannelOption{func(m js.M) { m["ordered"] = false }}

func MaxPacketLifeTime(t uint16) DataChannelOption {
	return DataChannelOption{func(m js.M) { m["maxPacketLifeTime"] = t }}
}
func MaxRetransmits(r uint16) DataChannelOption {
	return DataChannelOption{func(m js.M) { m["maxRetransmits"] = r }}
}
func Protocol(p string) DataChannelOption {
	return DataChannelOption{func(m js.M) { m["protocol"] = p }}
}
func Negotiated(id uint16) DataChannelOption {
	return DataChannelOption{func(m js.M) {
		m["negotiated"] = true
		m["id"] = id
	}}
}
