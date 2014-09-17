package main

import (
	rtl "github.com/jpoirier/gortlsdr"
	"log"
	// "unsafe"
)

func main() {
	var err error
	var dev *rtl.Context
	if dev, err = rtl.Open(0); err != nil {
		log.Fatal("\tOpen Failed, exiting\n")
	}
	defer dev.Close()

	err = dev.SetCenterFreq(92900000)
	if err != nil {
		log.Printf("\tSetCenterFreq 92.9MHz Failed, error: %s\n", err)
	} else {
		log.Printf("\tSetCenterFreq 92.9MHz Successful\n")
	}

	// mandatory, otherwise ReadSync fails with pipe error
	if err = dev.ResetBuffer(); err == nil {
		log.Printf("\tResetBuffer Successful\n")
	} else {
		log.Printf("\tResetBuffer Failed - error: %s\n", err)
	}

	var buffer []byte = make([]uint8, rtl.DefaultBufLength)
	n_read, err := dev.ReadSync(buffer, rtl.DefaultBufLength)
	if err != nil {
		log.Printf("\tReadSync Failed - error: %s, n_read %d\n", err, n_read)
	} else {
		log.Printf("\tReadSync %d\n", n_read)
	}

	log.Printf("Exiting...\n")
}
