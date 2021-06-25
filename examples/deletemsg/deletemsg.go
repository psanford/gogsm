package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/psanford/gogsm"
)

var modem = flag.String("modem", "/dev/ttyUSB1", "Modem device")
var idx = flag.Int("idx", -1, "Message Index")

func main() {
	flag.Parse()
	if *idx < 0 {
		log.Fatal("-idx is required")
	}
	m, err := gogsm.NewModem(*modem)
	if err != nil {
		log.Fatal(err)
	}
	err = m.Connect()
	if err != nil {
		log.Fatal(err)
	}

	err = m.DeleteMsg(*idx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("ok")
}
