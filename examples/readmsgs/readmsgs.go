package main

import (
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

	msgs, err := m.ReadMessages()
	if err != nil {
		log.Fatal(err)
	}
	for _, msg := range msgs {
		fmt.Printf("msg: %+v\n", msg)
	}
}
