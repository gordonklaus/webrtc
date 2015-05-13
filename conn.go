//+build js

package webrtc

import (
	"fmt"

	"github.com/gopherjs/gopherjs/js"
)

type Config struct {
	
}

type Conn struct {
	pc *js.Object
	
	iceCandidates      <-chan *ICECandidate
	iceConnectionState <-chan ICEConnectionState
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

	// In Firefox, a DataChannel apparently doesn't hold a strong references to its PeerConnection, so we must.
	if js.Global.Get("keepMeAlive") == js.Undefined {
		js.Global.Set("keepMeAlive", js.S{})
	}
	keepMeAlive := js.Global.Get("keepMeAlive")
	keepMeAlive.SetIndex(keepMeAlive.Length(), pc)

	return Conn{pc, iceCandidates, iceConnectionState}
}

type SignalingChannel interface {
	Recv() (Message, error)
	Send(Message) error
}

type Message struct {
	SessionDescription *SessionDescription
	ICECandidate       *ICECandidate
}

func (c Conn) Negotiate(initiate bool, sig SignalingChannel) error {
	if initiate {
		offer, err := c.createOffer()
		if err != nil {
			return err
		}
		err = c.setLocalDescription(offer)
		if err != nil {
			return err
		}
		err = sig.Send(Message{SessionDescription: &offer})
		if err != nil {
			return err
		}
	}
	messages := make(chan Message)
	recvErr := make(chan error)
	// TODO: Don't leak this goroutine (or those sending on other chans), in particular on return err.
	go func() {
		needDescription := true
		needICE := true
		for needDescription || needICE {
			m, err := sig.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			messages <- m
			if m.SessionDescription != nil {
				if m.SessionDescription.Type != ProvisionalAnswer {
					needDescription = false
				}
			} else if m.ICECandidate == nil {
				needICE = false
			}
		}
		close(messages)
	}()
	localICECandidates := c.iceCandidates
	var state ICEConnectionState
	for {
		select {
		case ic := <-localICECandidates:
			fmt.Println("sending", ic)
			err := sig.Send(Message{ICECandidate: ic})
			if err != nil {
				return err
			}
			if ic == nil {
				localICECandidates = nil
				if messages == nil {
					goto done
				}
			}
		case m, ok := <-messages:
			if !ok {
				messages = nil
				if localICECandidates == nil {
					goto done
				}
			}
			if m.SessionDescription != nil {
				fmt.Println("received", m.SessionDescription.Type)
				err := c.setRemoteDescription(*m.SessionDescription)
				if err != nil {
					return err
				}
				if !initiate {
					answer, err := c.createAnswer()
					if err != nil {
						return err
					}
					err = c.setLocalDescription(answer)
					if err != nil {
						return err
					}
					err = sig.Send(Message{SessionDescription: &answer})
					if err != nil {
						return err
					}
				}
			} else if m.ICECandidate != nil {
				fmt.Println("received", m.ICECandidate)
				err := c.addRemoteICECandidate(*m.ICECandidate)
				if err != nil {
					return err
				}
			}
		case err := <-recvErr:
			return err
		case state = <-c.iceConnectionState:
			// Don't return yet on Connected because we may still be gathering candidates.
			if state.Failed() {
				return fmt.Errorf("negotiation failed")
			}
		}
	}

done:
	// Completed is not yet reliably reported, so settle for Connected.
	if state.Connected() || state.Completed() {
		return nil
	}
	for state := range c.iceConnectionState {
		fmt.Println("ICE connection state:", state)
		switch {
		case state.Failed():
			return fmt.Errorf("negotiation failed")
		case state.Connected(), state.Completed():
			return nil
		}
	}
	panic("unreachable")
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

func (c Conn) addRemoteICECandidate(cand ICECandidate) error {
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

func (c Conn) createOffer() (offer SessionDescription, err error)   { return c.createSessionDescription(Offer) }
func (c Conn) createAnswer() (answer SessionDescription, err error) { return c.createSessionDescription(Answer) }

func (c Conn) createSessionDescription(typ SessionDescriptionType) (d SessionDescription, err error) {
	done := make(chan int)
	c.pc.Call("create" + string(typ), func(d_ *js.Object) {
		d.Type = typ
		d.SDP = d_.Get("sdp").String()
		close(done)
	}, func(err_ *js.Object) {
		err = errorFromJS(err_)
		close(done)
	})
	<-done
	return
}

func (c Conn) setLocalDescription(d SessionDescription) error  { return c.setDescription("Local", d) }
func (c Conn) setRemoteDescription(d SessionDescription) error { return c.setDescription("Remote", d) }

func (c Conn) setDescription(localRemote string, d SessionDescription) error {
	err := make(chan error)
	c.pc.Call("set" + localRemote + "Description", d.toJS(), func() {
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

type SessionDescriptionType string

func (t SessionDescriptionType) toJS() string {
	return map[SessionDescriptionType]string{
		Offer:             "offer",
		ProvisionalAnswer: "pranswer",
		Answer:            "answer",
	}[t]
}

const (
	Offer SessionDescriptionType = "Offer"
	ProvisionalAnswer            = "ProvisionalAnswer"
	Answer                       = "Answer"
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
