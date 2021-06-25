package gogsm

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
		conn:  s,
		lines: make(chan string, 10),
	}
	go m.lineReader()

	return &m, nil
}

type Modem struct {
	conn    *serial.Port
	lines   chan string
	readErr error
}

var CommandError = errors.New("cmd error")
var CommandNotSupportError = errors.New("cmd not support")

func (m *Modem) lineReader() {
	r := bufio.NewReader(m.conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			m.readErr = err
			close(m.lines)
			return
		}
		// fmt.Printf("read: %s", line)
		m.lines <- line
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

func (m *Modem) ReadMessages() ([]Msg, error) {
	var msgs []Msg
	lines, err := m.Command("AT+CMGL=4")
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(line, "+CMGL: ") {
			continue
		}
		line = strings.TrimPrefix(line, "+CMGL: ")
		record, err := csv.NewReader(bytes.NewBufferString(line)).Read()
		if err != nil {
			return nil, fmt.Errorf("parse +CMGL line %q err: %w", line, err)
		}
		idxS := record[0]
		statusS := record[1]
		// name := record[2]
		// pduLen := record[3]

		idx, err := strconv.Atoi(idxS)
		if err != nil {
			return nil, fmt.Errorf("parse +CMGL idx %q err: %w", line, err)
		}

		status, err := strconv.Atoi(statusS)
		if err != nil {
			return nil, fmt.Errorf("parse +CMGL status %q err: %w", line, err)
		}

		pduline := lines[i+1]
		i++
		pduline = pduline[:len(pduline)-2]

		pdu, err := pdumode.UnmarshalHexString(pduline)
		if err != nil {
			return nil, fmt.Errorf("parse +CMGL pdu %q err: %w", pduline, err)
		}

		var ops []sms.UnmarshalOption
		if status == int(StatusPendingSend) || status == int(StatusSent) {
			ops = append(ops, sms.AsMO) // originated from Mobile Station
		}
		p, err := sms.Unmarshal(pdu.TPDU, ops...)
		if err != nil {
			return nil, fmt.Errorf("parse +CMGL pdu %q err: %w", pduline, err)
		}

		msg := Msg{
			Index:   idx,
			Status:  MsgStatus(status),
			Inbound: p.Direction == tpdu.MT,
			From:    pdu.SMSC.Number(),
			TS:      p.SCTS.Time,
			Body:    string(p.UD),
		}

		msgs = append(msgs, msg)
	}
	return msgs, nil
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
