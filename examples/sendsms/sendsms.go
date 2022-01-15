package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/psanford/gogsm"
)

var modem = flag.String("modem", "/dev/ttyUSB1", "Modem device")
var number = flag.String("number", "", "Phone number")
var msg = flag.String("msg", "", "Message to send")

func main() {
	flag.Parse()
	if *number == "" {
		log.Fatalf("-number is required")
	}

	if *msg == "" {
		log.Fatalf("-msg is required")
	}

	if !strings.HasPrefix(*number, "+") {
		log.Fatalf("-number must be in the format +12035248123")
	}

	m, err := gogsm.NewModem(*modem)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Connect to modem")
	err = m.Connect()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Send msg")
	err = m.SendSMS(*number, *msg)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("ok")
}
