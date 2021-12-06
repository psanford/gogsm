package gogsm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tarm/serial"
	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/pdumode"
	"github.com/warthog618/sms/encoding/tpdu"
)

func NewModem(serialPort string) (*Modem, error) {
	c := &serial.Config{Name: serialPort, Baud: 9600}
	s, err := serial.OpenPort(c)
	if err != nil {
		return nil, err
	}

	m := Modem{
		conn:       s,
		lines:      make(chan string, 10),
		asyncLines: make(chan string, 10),
	}
	go m.lineReader()
	go m.handleAsyncLines()

	return &m, nil
}

type Modem struct {
	conn          *serial.Port
	lines         chan string
	asyncLines    chan string
	readErr       error
	mu            sync.Mutex
	eventHandlers []eventHandler
}

var CommandError = errors.New("cmd error")
var CommandNotSupportError = errors.New("cmd not support")

func (m *Modem) handleAsyncLines() {
	for line := range m.asyncLines {
		if strings.HasPrefix(line, "^") {
			continue
		} else if strings.HasPrefix(line, "+CMTI:") {
			m.mu.Lock()
			var smsHandlers []eventHandler
			for i, h := range m.eventHandlers {
				if h.ctx.Err() != nil {
					m.eventHandlers = append(m.eventHandlers[:i], m.eventHandlers[i+1:]...)
				}
				if h.evtType&EvtSMS > 0 {
					smsHandlers = append(smsHandlers, h)
				}
			}
			m.mu.Unlock()

			if len(smsHandlers) > 0 {
				// +CMTI: "ME",7
				_, idx, err := parseCMTI(line)
				if err != nil {
					log.Printf("parseCMTI err: %s", err)
					continue
				}

				msg, err := m.ReadMsg(idx)
				if err != nil {
					log.Printf("readMsg err: %s", err)
					continue
				}
				evt := AsyncEvent{
					Type: EvtSMS,
					Msg:  *msg,
				}

				for _, h := range smsHandlers {
					select {
					case h.c <- evt:
					case <-h.ctx.Done():
					}
				}
			}
		}
	}
}

func (m *Modem) lineReader() {
	r := bufio.NewReader(m.conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			m.readErr = err
			close(m.lines)
			return
		}
		// log.Printf("read: %s", line)
		if strings.HasPrefix(line, "^") || strings.HasPrefix(line, "+CMTI:") {
			m.asyncLines <- line
			continue
		}
		select {
		case m.lines <- line:
		default:
			// log.Printf("line dropped")
		}
	}
}

func (m *Modem) Command(d string) ([]string, error) {
	_, err := m.write([]byte(d))
	if err != nil {
		return nil, err
	}
	return m.ReadUntilOkError()
}

func (m *Modem) ReadUntilOkError() ([]string, error) {
	okMsg := "OK\r\n"
	errorMsg := "ERROR\r\n"
	notSupportMsg := "COMMAND NOT SUPPORT\r\n"

	lines := make([]string, 0, 2)
	for {
		line, ok := <-m.lines
		if !ok {
			return lines, m.readErr
		}
		lines = append(lines, line)
		if line == okMsg {
			return lines, nil
		} else if line == errorMsg {
			return lines, CommandError
		} else if line == notSupportMsg {
			return lines, CommandNotSupportError
		}
	}
}

type MsgStatus int

const (
	StatusRecvUnread  MsgStatus = 0
	StatusRecvRead    MsgStatus = 1
	StatusPendingSend MsgStatus = 2
	StatusSent        MsgStatus = 3
)

type Msg struct {
	Index   int
	Status  MsgStatus
	Inbound bool
	From    string
	To      string
	TS      time.Time
	Body    string
}

type AsyncEventType int

const (
	EvtSMS AsyncEventType = 1 << iota
	// EvtUSSD AsyncEventType = 1 << iota
	// EvtDTMF AsyncEventType = 1 << iota
)

type AsyncEvent struct {
	Type AsyncEventType
	Msg  Msg
}

type eventHandler struct {
	evtType AsyncEventType
	c       chan AsyncEvent
	ctx     context.Context
}

func (m *Modem) Subscribe(ctx context.Context, evtMask AsyncEventType) (chan AsyncEvent, error) {
	if evtMask&EvtSMS > 0 {
		_, err := m.Command("AT+CNMI=2,1,0,2")
		if err != nil {
			return nil, err
		}
	}

	h := eventHandler{
		evtType: evtMask,
		c:       make(chan AsyncEvent),
		ctx:     ctx,
	}

	m.mu.Lock()
	m.eventHandlers = append(m.eventHandlers, h)
	m.mu.Unlock()

	return h.c, nil
}

func (m *Modem) DeleteMsg(idx int) error {
	_, err := m.Command(fmt.Sprintf("AT+CMGD=%d", idx))
	return err
}

func (m *Modem) ReadMessages() ([]Msg, error) {
	lines, err := m.Command("AT+CMGL=4")
	if err != nil {
		return nil, err
	}
	return m.parseMsgs(lines, "+CMGL: ")
}

func (m *Modem) parseMsgs(lines []string, prefix string) ([]Msg, error) {
	var msgs []Msg
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		line = strings.TrimPrefix(line, prefix)
		record, err := csv.NewReader(bytes.NewBufferString(line)).Read()
		if err != nil {
			return nil, fmt.Errorf("parse %s line %q err: %w", prefix, line, err)
		}
		idxS := record[0]
		statusS := record[1]
		// name := record[2]
		// pduLen := record[3]

		idx, err := strconv.Atoi(idxS)
		if err != nil {
			return nil, fmt.Errorf("parse %s idx %q err: %w", prefix, line, err)
		}

		var status int
		if prefix == "+CMGL: " {
			status, err = strconv.Atoi(statusS)
			if err != nil {
				return nil, fmt.Errorf("parse %s status %q err: %w", prefix, line, err)
			}
		}

		pduline := lines[i+1]
		i++
		pduline = pduline[:len(pduline)-2]

		pdu, err := pdumode.UnmarshalHexString(pduline)
		if err != nil {
			return nil, fmt.Errorf("parse %s pdu %q err: %w", prefix, pduline, err)
		}

		var ops []sms.UnmarshalOption
		if status == int(StatusPendingSend) || status == int(StatusSent) {
			ops = append(ops, sms.AsMO) // originated from Mobile Station
		}
		p, err := sms.Unmarshal(pdu.TPDU, ops...)
		if err != nil {
			return nil, fmt.Errorf("parse %s pdu %q err: %w", prefix, pduline, err)
		}

		msg := Msg{
			Index:   idx,
			Status:  MsgStatus(status),
			Inbound: p.Direction == tpdu.MT,
			From:    p.OA.Number(),
			TS:      p.SCTS.Time,
			Body:    string(p.UD),
		}

		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func (m *Modem) ReadMsg(idx int) (*Msg, error) {
	lines, err := m.Command(fmt.Sprintf("AT+CMGR=%d", idx))
	if err != nil {
		return nil, err
	}
	msgs, err := m.parseMsgs(lines, "+CMGR: ")
	if err != nil {
		return nil, err
	}
	if len(msgs) < 1 {
		return nil, errors.New("no message found")
	}
	msg := msgs[0]
	return &msg, nil
}

func (m *Modem) Connect() error {
	_, err := m.Command("AT")
	if err != nil {
		return err
	}

	// echo off
	_, err = m.Command("ATE0")
	if err != nil {
		return err
	}

	// PDU mode
	m.Command("AT+CMFG=0")
	// _, err = m.Command("AT+CMFG=0")
	// if err != nil {
	// 	return err
	// }

	_, err = m.Command(`AT+CPMS="ME","SM"`)
	if err != nil {
		return err
	}

	return nil
}

func (m *Modem) writeStr(b string) (int, error) {
	return m.write([]byte(b))
}

func (m *Modem) write(b []byte) (int, error) {
	d := make([]byte, len(b)+2)
	copy(d, b)
	d[len(d)-2] = '\r'
	d[len(d)-1] = '\n'
	l, err := m.conn.Write(d)
	if l == len(d) {
		return l - 2, err
	}
	return l, err
}

// Parse +CMTI lines (Incoming SMS event)
func parseCMTI(line string) (string, int, error) {
	line = strings.TrimPrefix(line, "+CMTI: ")
	record, err := csv.NewReader(bytes.NewBufferString(line)).Read()
	if err != nil {
		return "", -1, fmt.Errorf("parse +CMTI line %q err: %w", line, err)
	}

	idx, err := strconv.Atoi(record[1])
	if err != nil {
		return "", 01, fmt.Errorf("parse +CMTI idx %q err: %w", line, err)
	}

	return record[0], idx, nil
}
