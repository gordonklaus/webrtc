//+build js

package webrtc

import (
	"fmt"
	// "runtime"
	// "strings"

	"github.com/gopherjs/gopherjs/js"
)

type Config struct {
	
}

type Conn struct {
	pc *js.Object
	
	ICECandidates      <-chan *ICECandidate
	ICEConnectionState <-chan ICEConnectionState
}

func NewConn(config Config) Conn {
	cfg := js.M{"iceServers": js.S{}}
	pc := getType("RTCPeerConnection").New(cfg)
	iceCandidates := make(chan *ICECandidate)
	pc.Set("onicecandidate", func(e *js.Object) {
		cand := e.Get("candidate")
		var c *ICECandidate
		if cand != nil {
			c = &ICECandidate{
				Candidate:     cand.Get("candidate").String(),
				SDPMid:        cand.Get("sdpMid").String(),
				SDPMLineIndex: uint16(cand.Get("sdpMLineIndex").Int()),
			}
		}
		go func() {
			iceCandidates <- c
		}()
	})
	iceConnectionState := make(chan ICEConnectionState)
	pc.Set("oniceconnectionstatechange", func(e *js.Object) {
		go func() {
			iceConnectionState <- ICEConnectionState(pc.Get("iceConnectionState").String())
		}()
	})
	// pc.Set("onaddstream", func(e *js.Object) {
	//     fmt.Println("stream added:", e)
	// })
	return Conn{pc, iceCandidates, iceConnectionState}
}

func (c Conn) Close() {
	c.pc.Call("close")
}

type ICECandidate struct {
	Candidate     string
	SDPMid        string
	SDPMLineIndex uint16
}

func (c ICECandidate) toJS() *js.Object {
	return getType("RTCIceCandidate").New(js.M{
		"candidate":     c.Candidate,
		"sdpMid":        c.SDPMid,
		"sdpMLineIndex": c.SDPMLineIndex,
	})
}

func (c Conn) AddICECandidate(cand ICECandidate) error {
	err := make(chan error)
	c.pc.Call("addIceCandidate", cand.toJS(), func() {
		err <- nil
	}, func(err_ *js.Object) {
		err <- errorFromJS(err_)
	})
	return <-err
}

type ICEConnectionState string

func (s ICEConnectionState) New() bool { return s == "new" }
func (s ICEConnectionState) Checking() bool { return s == "checking" }
func (s ICEConnectionState) Connected() bool { return s == "connected" }
func (s ICEConnectionState) Completed() bool { return s == "completed" }
func (s ICEConnectionState) Failed() bool { return s == "failed" }
func (s ICEConnectionState) Disconnected() bool { return s == "disconnected" }
func (s ICEConnectionState) Closed() bool { return s == "closed" }

type DataChannel struct {
	dc *js.Object

	open  chan struct{}
	recv  chan string
	close chan struct{}
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
		// defer catchCloseOfClosedChannel()
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

	// seems unnecessary, but keep it for awhile just in case
	// defer catchCloseOfClosedChannel()
	// close(d.close)
}

func catchCloseOfClosedChannel() {
	// if x := recover(); x != nil {
	// 	if err, ok := x.(runtime.Error); !ok || !strings.Contains(err.Error(), "close of closed channel") {
	// 		panic(x)
	// 	}
	// }
}

func (c Conn) CreateOffer() (offer SessionDescription, err error) {
	done := make(chan int)
	c.pc.Call("createOffer", func(offer_ *js.Object) {
		offer.Type = Offer
		offer.SDP = offer_.Get("sdp").String()
		close(done)
	}, func(err_ *js.Object) {
		err = errorFromJS(err_)
		close(done)
	})
	<-done
	return
}

func (c Conn) CreateAnswer() (answer SessionDescription, err error) {
	done := make(chan int)
	c.pc.Call("createAnswer", func(answer_ *js.Object) {
		answer.Type = Answer
		answer.SDP = answer_.Get("sdp").String()
		close(done)
	}, func(err_ *js.Object) {
		err = errorFromJS(err_)
		close(done)
	})
	<-done
	return
}

func (c Conn) SetLocalDescription(desc SessionDescription) error {
	err := make(chan error)
	c.pc.Call("setLocalDescription", desc.toJS(), func() {
		err <- nil
	}, func(err_ *js.Object) {
		err <- errorFromJS(err_)
	})
	return <-err
}

func (c Conn) SetRemoteDescription(desc SessionDescription) error {
	err := make(chan error)
	c.pc.Call("setRemoteDescription", desc.toJS(), func() {
		err <- nil
	}, func(err_ *js.Object) {
		err <- errorFromJS(err_)
	})
	return <-err
}

func errorFromJS(err *js.Object) error {
	return fmt.Errorf("%v", err.String())
}

type SessionDescription struct {
	Type SessionDescriptionType
	SDP  string
}

func (d SessionDescription) toJS() *js.Object {
	return getType("RTCSessionDescription").New(js.M{
		"type": d.Type.toJS(),
		"sdp":  d.SDP,
	})
}

type SessionDescriptionType byte

func (t SessionDescriptionType) String() string {
	return map[SessionDescriptionType]string{
		Offer:             "Offer",
		ProvisionalAnswer: "ProvisionalAnswer",
		Answer:            "Answer",
	}[t]
}

func (t SessionDescriptionType) toJS() string {
	return map[SessionDescriptionType]string{
		Offer:             "offer",
		ProvisionalAnswer: "pranswer",
		Answer:            "answer",
	}[t]
}

const (
	_ SessionDescriptionType = iota
	Offer
	ProvisionalAnswer
	Answer
)

func getType(name string) *js.Object {
	t := js.Global.Get(name)
	if t == js.Undefined {
		t = js.Global.Get("webkit" + name)
	}
	if t == js.Undefined {
		t = js.Global.Get("moz" + name)
	}
	if t == js.Undefined {
		panic("type " + name + " not found")
	}
	return t
}
