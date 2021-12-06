package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/psanford/gogsm"
)

var modem = flag.String("modem", "/dev/ttyUSB1", "Modem device")

func main() {
	flag.Parse()
	m, err := gogsm.NewModem(*modem)
	if err != nil {
		log.Fatal(err)
	}
	err = m.Connect()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evtCh, err := m.Subscribe(ctx, gogsm.EvtSMS)
	if err != nil {
		log.Fatal(err)
	}

	for evt := range evtCh {
		fmt.Printf("Got %+v\n", evt)
	}
}
