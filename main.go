package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	//	"time"

	rtl "github.com/jpoirier/gortlsdr"
)

const (
	defaultSampleRate = 24000
	defaultBufLen     = (1 * 16384)
	maximumOversample = 16
	maximumBufLen     = (maximumOversample * defaultBufLen)
	autoGain          = -100
	bufferDump        = 4096

	//cicTableMax = 10

	frequenciesLimit = 1000
)

type frequencies []uint32
type exitChan chan struct{}

type dongleState struct {
	dev            *rtl.Context
	devIndex       int
	freq           uint32
	rate           uint32
	gain           int
	ppmError       int
	offsetTuning   bool
	directSampling int
	mute           int
	demodTarget    *demodState
}

type demodState struct {
	lowpassed  []int16
	lpIHist    [10][6]int16
	lpQHist    [10][6]int16
	result     [maximumBufLen]int16 // ?
	resultLen  int                  // ?
	lpChan     chan []int16
	droopIHist [9]int16
	droopQHist [9]int16
	rateIn     int
	rateOut    int
	//rateOut2           int
	nowR               int
	nowJ               int
	preR               int
	preJ               int
	prevIndex          int
	downsample         int // min 1, max 256
	postDownsample     int
	outputScale        int
	squelchLevel       int
	conseqSquelch      int
	squelchHits        int
	terminateOnSquelch int
	downsamplePasses   int
	compFirSize        int
	customAtan         int
	deemph, deemphA    int
	nowLpr             int
	prevLprIndex       int
	dcBlock, dcAvg     int
	modeDemod          func(fm *demodState)
	outputTarget       *outputState
}

type outputState struct {
	file      *os.File
	filename  string
	result    [maximumBufLen]int16 // ?
	resultLen int                  // ?
	rate      int

	resultChan chan []int16
}

type controllerState struct {
	freqs   frequencies
	freqNow int
	edge    int
}

var dongle *dongleState
var demod *demodState
var output *outputState
var controller *controllerState

var actualBufLen int
var lcmPost = [17]int{1, 1, 1, 3, 1, 5, 3, 7, 1, 9, 5, 11, 3, 13, 7, 15, 1}

func init() {

	dongle = &dongleState{}
	demod = &demodState{}
	output = &outputState{}
	controller = &controllerState{}

	dongle.Init()
	demod.Init()
	output.Init()
	controller.Init()
}

func (s *dongleState) Init() {
	s.rate = defaultSampleRate
	s.gain = autoGain // tenths of a dB
	s.demodTarget = demod
}

func (s *demodState) Init() {
	s.rateIn = defaultSampleRate
	s.rateOut = defaultSampleRate
	s.conseqSquelch = 10
	s.squelchHits = 11
	s.postDownsample = 1 // once this works, default = 4
	//s.modeDemod = fmDemod
	s.outputTarget = output

	s.lpChan = make(chan []int16)
}

func (s *controllerState) Init() {
	s.freqs = append(s.freqs, 100000000)
}

func (s *outputState) Init() {
	s.rate = defaultSampleRate
	s.resultChan = make(chan []int16)
}

func (f frequencies) String() string {
	return fmt.Sprintf("%d", f)
}

func (f *frequencies) Set(val string) (err error) {
	var i int
	var start, stop, step int

	step = 25000

	bits := strings.Split(val, ":")

	fmt.Fprintf(os.Stderr, "bits len %d\n", len(bits))

	switch len(bits) {
	case 1:
		i, err = strconv.Atoi(bits[0])
		if err != nil {
			return
		}
		freqs := *f
		freqs = append(freqs, uint32(i))
		*f = freqs
		return
	case 3:
		step, err = strconv.Atoi(bits[2])
		if err != nil {
			return
		}
		fallthrough
	case 2:
		start, err = strconv.Atoi(bits[0])
		if err != nil {
			return
		}
		stop, err = strconv.Atoi(bits[1])
		if err != nil {
			return
		}
	default:
		err = fmt.Errorf("Frequency range could not be parsed")
		return
	}

	for j := start; j <= stop; j += step {
		freqs := *f
		if len(freqs) > frequenciesLimit {
			break
		}
		freqs = append(freqs, uint32(j))
		*f = freqs
	}
	return
}

func rtlsdrCallback(buf []byte, ctx *rtl.UserCtx) {
	var i int
	var quit exitChan
	var ok bool

	if quit, ok = (*ctx).(exitChan); !ok {
		fmt.Fprintf(os.Stderr, "rtlsdr callback, channel not recognised\n")
		return
	}

	select {

	case <-quit:
		fmt.Fprintf(os.Stderr, "rtlsdr callback, channel closed\n")
		return
	default:

		if dongle.mute > 0 && dongle.mute < len(buf) {
			for i = 0; i < dongle.mute; i++ {
				buf[i] = 127
			}
			dongle.mute = 0
		}
		/*
			if dongleS.offsetTuning {
				//rotate_90(buf, len)
			}
		*/
		// size of int16
		var s = 2
		buf16 := make([]int16, len(buf)/s)
		for i := range buf16 {
			samp := int16(binary.LittleEndian.Uint16(buf[i*s : (i+1)*s]))
			buf16[i] = samp - 127
		}
		demod.lpChan <- buf16
	}
}

// run as goroutine
// ReadAsync blocks until CancelAsync
func dongleRoutine(wg *sync.WaitGroup, quit exitChan) {
	var userctx rtl.UserCtx = quit

	defer wg.Done()
	err := dongle.dev.ReadAsync(rtlsdrCallback, &userctx, rtl.DefaultAsyncBufNumber, rtl.DefaultBufLength)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ReadAsync failed, err %s\n", err)
	}

	fmt.Fprintf(os.Stderr, "Returning from dongleRoutine\n")
}

// run as goroutine
func demodRoutine(wg *sync.WaitGroup, quit exitChan) {
	d := demod
	o := d.outputTarget
	defer wg.Done()

	var lp []int16

	for {
		select {

		case <-quit:
			fmt.Fprintf(os.Stderr, "Returning from demodRoutine\n")
			return

		case lp = <-demod.lpChan:

			d.lowpassed = lp

			//lock?
			d.fullDemod()
			//unlock?

			if d.squelchLevel > 0 && d.squelchHits > d.conseqSquelch {
				// hair trigger
				d.squelchHits = d.conseqSquelch + 1
				continue
			}
			//memcpy(o->result, d->result, 2*d->result_len);
			var result []int16
			copy(result, d.result[:])
			o.resultChan <- result
		}
	}
}

// thoughts for multiple dongles
// might be no good using a controller thread if retune/rate blocks
func controllerRoutine(wg *sync.WaitGroup, quit exitChan) {
	var err error

	defer wg.Done()

	s := controller

	// set up primary channel
	optimalSettings(int(s.freqs[0]), demod.rateIn)

	// Set the frequency
	err = dongle.dev.SetCenterFreq(int(dongle.freq))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting frequency %d\n", dongle.freq)
		return
	}
	//fmt.Fprintf(os.Stderr, "Oversampling input by: %ix.\n", demod.downsample)
	//fmt.Fprintf(os.Stderr, "Oversampling output by: %ix.\n", demod.postDownsample)
	fmt.Fprintf(os.Stderr, "Buffer size: %0.2fms\n", 1000*0.5*float32(actualBufLen)/float32(dongle.rate))

	// Set the sample rate
	err = dongle.dev.SetSampleRate(int(dongle.rate))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting sample rate %d\n", dongle.rate)
		return
	}
	fmt.Fprintf(os.Stderr, "Output at %d Hz.\n", demod.rateIn/demod.postDownsample)

	for {
		select {

		case <-quit:
			fmt.Fprintf(os.Stderr, "Returning from controllerRoutine\n")
			if err := dongle.dev.CancelAsync(); err != nil {
				fmt.Fprintf(os.Stderr, "Error canceling async %s\n", err)
			}
			return

		default:

			if len(s.freqs) <= 1 {
				continue
			}
			// hacky hopping
			s.freqNow = (s.freqNow + 1) % len(s.freqs)
			optimalSettings(int(s.freqs[s.freqNow]), demod.rateIn)
			err = dongle.dev.SetCenterFreq(int(dongle.freq))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error setting frequency %d\n", dongle.freq)
				return
			}
			dongle.mute = bufferDump
		}
	}
}

func outputRoutine(wg *sync.WaitGroup, quit exitChan) {
	var err error

	defer wg.Done()
	for {
		select {

		case <-quit:
			fmt.Fprintf(os.Stderr, "Returning from outputRoutine\n")
			return

		case result := <-output.resultChan:
			err = binary.Write(output.file, binary.LittleEndian, result)
			if err != nil {
				fmt.Fprintf(os.Stderr, "output write error %s\n", err)
			}
		}
	}
}

func (d *demodState) fullDemod() {
	var i int

	lowPass(d)

	// power squelch
	if d.squelchLevel > 0 {
		sr := rms(d.lowpassed, 1)
		if sr < d.squelchLevel {
			d.squelchHits++
			for i = 0; i < len(d.lowpassed); i++ {
				d.lowpassed[i] = 0
			}
		} else {
			d.squelchHits = 0
		}
	}

	d.modeDemod(d)
}

func main() {
	var err error

	flag.IntVar(&dongle.devIndex, "d", 0, "dongle device index")
	flag.Var(&controller.freqs, "f", "frequency or range of frequencies, and step e.g 92900:100100:25000")
	flag.IntVar(&demod.squelchLevel, "l", 0, "squelch level")
	rateIn := flag.Int("s", 0, "sample rate")
	flag.IntVar(&dongle.ppmError, "p", 0, "ppm error")
	demodMode := flag.String("M", "am", "demodulation mode [fm, am]")

	flag.Parse()

	switch *demodMode {
	case "fm":
		demod.modeDemod = fmDemod
	case "am":
		demod.modeDemod = amDemod
	default:
		demod.modeDemod = amDemod
	}

	if *rateIn > 0 {
		demod.rateIn = *rateIn
	}

	demod.rateOut = demod.rateIn

	if len(controller.freqs) == 0 {
		fmt.Fprintf(os.Stderr, "Please specify a frequency.")
		return
	}

	if len(controller.freqs) >= frequenciesLimit {
		fmt.Fprintf(os.Stderr, "Too many channels, maximum %d.\n", frequenciesLimit)
		return
	}

	if len(controller.freqs) > 1 && demod.squelchLevel == 0 {
		fmt.Fprintf(os.Stderr, "Please specify a squelch level.  Required for scanning multiple frequencies.")
		return
	}

	/* quadruple sample_rate to limit to Δθ to ±π/2 */
	demod.rateIn *= demod.postDownsample

	if output.rate == 0 {
		output.rate = demod.rateOut
	}

	if len(controller.freqs) > 1 {
		demod.terminateOnSquelch = 0
	}

	if flag.Arg(0) != "" {
		output.filename = flag.Arg(0)
	} else {
		output.filename = "-"
	}

	actualBufLen = lcmPost[demod.postDownsample] * defaultBufLen

	dongle.dev, err = rtl.Open(dongle.devIndex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open dongle, '%s', exiting\n", err)
		return
	}
	defer dongle.dev.Close()

	/* Set the tuner gain */
	if dongle.gain == autoGain {
		err = dongle.dev.SetTunerGainMode(false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting tuner auto-gain: %s\n", err)
			return
		}
	} else {
		dongle.gain, err = nearestGain(dongle.dev, dongle.gain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting nearest gain to %d: %s\n", dongle.gain, err)
			return
		}
		err = dongle.dev.SetTunerGain(dongle.gain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting tuner manual gain to %d: %s\n", dongle.gain)
			return
		}
	}

	if dongle.ppmError > 0 {
		err = dongle.dev.SetFreqCorrection(dongle.ppmError)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting frequency correction to %d: %s\n", dongle.ppmError, err)
			return
		}
	}

	if output.filename == "-" {
		output.file = os.Stdout
	} else {
		output.file, err = os.OpenFile(output.filename, os.O_RDWR|os.O_APPEND, 0660)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	defer output.file.Close()

	// Reset endpoint before we start reading from it (mandatory)
	err = dongle.dev.ResetBuffer()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	signalChan := make(chan os.Signal, 1)
	quit := make(exitChan)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		for _ = range signalChan {
			fmt.Fprintln(os.Stderr, "\nReceived an interrupt, stopping services...")
			close(quit)
		}
	}()
	var wg sync.WaitGroup

	wg.Add(4)

	go controllerRoutine(&wg, quit)
	//time.Sleep(1)
	go outputRoutine(&wg, quit)
	go demodRoutine(&wg, quit)
	go dongleRoutine(&wg, quit)

	dongle.dev.CancelAsync()

	wg.Wait()

	fmt.Fprintf(os.Stderr, "Exiting...\n")
}
