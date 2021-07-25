package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"goplay2/globals"
	"goplay2/ptp"
	"io"
	"time"
)

type PlaybackStatus uint8

const (
	STOPPED PlaybackStatus = iota
	PLAYING
)

var underflow = errors.New("audio underflow")

type StreamCallback func(out []int16, currentTime time.Duration, outputBufferDacTime time.Duration)

type Stream interface {
	io.Closer
	Init(callBack StreamCallback) error
	Start() error
	Stop() error
	SetVolume(volume float64)
}

type Player struct {
	ControlChannel chan globals.ControlMessage
	clock          *Clock
	Status         PlaybackStatus
	stream         Stream
	ringBuffer     *Ring
}

func NewPlayer(clock *ptp.VirtualClock, ring *Ring) *Player {

	return &Player{
		clock:          &Clock{clock, time.Now(), 0, 0},
		ControlChannel: make(chan globals.ControlMessage, 100),
		stream:         NewStream(),
		Status:         STOPPED,
		ringBuffer:     ring,
	}
}

func (p *Player) callBack(out []int16, currentTime time.Duration, outputBufferDacTime time.Duration) {
	rtpTime := p.clock.CurrentRtpTime()
	frame, err := p.ringBuffer.TryPeek()
	if err == ErrIsEmpty || int64(frame.(*PCMFrame).Timestamp) > rtpTime {
		p.fillSilence(out)
	} else {
		frame, err = p.ringBuffer.TryPop()
		for err != ErrIsEmpty && int64(frame.(*PCMFrame).Timestamp) < rtpTime-1024 {
			frame, err = p.ringBuffer.TryPop()
		}
		if err == ErrIsEmpty {
			p.fillSilence(out)
		} else {
			err = binary.Read(bytes.NewReader(frame.(*PCMFrame).pcmData), binary.LittleEndian, out)
			if err != nil {
				globals.ErrLog.Printf("error reading data : %v\n", err)
			}
		}
	}
	p.clock.IncRtpTime()
}

func (p *Player) Run() {
	var err error
	if err := p.stream.Init(p.callBack); err != nil {
		globals.ErrLog.Fatalln("Audio Stream init error:", err)
	}
	defer p.stream.Close()
	for {
		select {
		case msg := <-p.ControlChannel:
			switch msg.MType {
			case globals.PAUSE:
				if p.Status == PLAYING {
					if err := p.stream.Stop(); err != nil {
						globals.ErrLog.Printf("error pausing audio :%v\n", err)
						return
					}
				}
				p.Status = STOPPED
			case globals.START:
				if p.Status == STOPPED {
					err = p.stream.Start()
					if err != nil {
						globals.ErrLog.Printf("error while starting flow : %v\n", err)
						return
					}
				}
				p.Status = PLAYING
				p.clock.AnchorTime(msg.Param1, msg.Param2)
			case globals.SKIP:
				p.skipUntil(msg.Param1, msg.Param2)
			case globals.VOLUME:
				p.stream.SetVolume(msg.Paramf)
			}
		}
	}
}

func (p *Player) skipUntil(fromSeq int64, UntilSeq int64) {
	p.ringBuffer.Flush(func(value interface{}) bool {
		frame := value.(*PCMFrame)
		return frame.SequenceNumber < uint32(fromSeq) || frame.SequenceNumber > uint32(UntilSeq)
	})
}

func (p *Player) Push(frame interface{}) {
	p.ringBuffer.Push(frame)
}

func (p *Player) Reset() {
	p.ringBuffer.Reset()
}

func (p *Player) fillSilence(out []int16) {
	for i := range out {
		out[i] = 0
	}
}
