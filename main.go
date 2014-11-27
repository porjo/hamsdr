// Copyright (C) 2014 Ian Bishop
//
// This program is free software; you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation; either version 2 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License along
// with this program; if not, write to the Free Software Foundation, Inc.,
// 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.

// Package hamsdr implements a software-defined radio scanner
//
// hamsdr requires rtlsdr library
//
package main

import (
	//	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	rtl "github.com/jpoirier/gortlsdr"
)

var dongleTimer time.Time

const (
	defaultSampleRate = 24000
	defaultBufLen     = (1 * 16384)
	maximumOversample = 16
	maximumBufLen     = (maximumOversample * defaultBufLen)
	autoGain          = -100
	bufferDump        = 4096
	minimumRate       = 1000000

	//cicTableMax = 10

	frequenciesLimit = 1000
)

// used to parse multiple -f params
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
	lpChan         chan []int16
	preRotate      bool
}

type demodState struct {
	lowpassed          []int16
	lpIHist            [10][6]int16
	lpQHist            [10][6]int16
	result             []int16 // ?
	droopIHist         [9]int16
	droopQHist         [9]int16
	rateIn             int
	rateOut            int
	rateOut2           int
	nowR               int16
	nowJ               int16
	preR               int16
	preJ               int16
	prevIndex          int
	downsample         int // min 1, max 256
	postDownsample     int
	outputScale        int
	squelchLevel       int
	conseqSquelch      int
	squelchHits        int
	terminateOnSquelch int
	//downsamplePasses   int
	compFirSize    int
	customAtan     int
	deemph         bool
	deemphA        int
	nowLpr         int
	prevLprIndex   int
	dcBlock, dcAvg int
	modeDemod      func(fm *demodState)
	agc            agcState
}

type outputState struct {
	file     *os.File
	filename string
	rate     int

	resultChan chan []int16
}

type controllerState struct {
	freqs   frequencies
	freqNow int
	edge    int
	wbMode  bool

	hopChan chan bool
}

type agcState struct {
	gainNum    int32
	gainDen    int32
	gainMax    int32
	peakTarget int
	attackStep int
	decayStep  int
	//	int     error;
}

var dongle *dongleState
var demod *demodState
var output *outputState
var controller *controllerState

var actualBufLen int
var lcmPost = [17]int{1, 1, 1, 3, 1, 5, 3, 7, 1, 9, 5, 11, 3, 13, 7, 15, 1}

func init() {
	dongle = &dongleState{}
	output = &outputState{}
	demod = &demodState{}
	controller = &controllerState{}

	dongle.rate = defaultSampleRate
	dongle.gain = autoGain // tenths of a dB
	dongle.demodTarget = demod
	dongle.lpChan = make(chan []int16, 1)
	dongle.preRotate = true

	demod.rateIn = defaultSampleRate
	demod.rateOut = defaultSampleRate
	demod.conseqSquelch = 10
	demod.squelchHits = 11
	demod.postDownsample = 1 // once this works, default = 4
	demod.agc.gainDen = 1 << 15
	demod.agc.gainNum = demod.agc.gainDen
	demod.agc.peakTarget = 1 << 14
	demod.agc.gainMax = 256 * demod.agc.gainDen
	demod.agc.decayStep = 1
	demod.agc.attackStep = -2

	output.rate = defaultSampleRate
	output.resultChan = make(chan []int16, 1)

	controller.hopChan = make(chan bool)
}

func setFreqs(val string) (freqs frequencies, err error) {
	if val == "" {
		return
	}

	var freq uint32
	var start, stop, step uint32

	step = 25000

	bits := strings.Split(val, ":")

	switch len(bits) {
	case 1:
		freq, err = freqHz(bits[0])
		if err != nil {
			return
		}
		freqs = append(freqs, freq)
		return
	case 3:
		step, err = freqHz(bits[2])
		if err != nil {
			return
		}
		fallthrough
	case 2:
		start, err = freqHz(bits[0])
		if err != nil {
			return
		}
		stop, err = freqHz(bits[1])
		if err != nil {
			return
		}
	default:
		err = fmt.Errorf("Frequency range could not be parsed")
		return
	}

	for j := start; j <= stop; j += step {
		if len(freqs) > frequenciesLimit {
			break
		}
		freqs = append(freqs, j)
	}

	return
}

// Convert frequency string to Hz
// 90.2M = 90200000
// 25K = 25000
func freqHz(freqStr string) (freq uint32, err error) {
	var f64 float64
	upper := strings.ToUpper(freqStr)

	switch {
	case strings.HasSuffix(upper, "K"):
		upper = strings.TrimSuffix(upper, "K")
		f64, err = strconv.ParseFloat(upper, 64)
		freq = uint32(f64 * 1e3)
	case strings.HasSuffix(upper, "M"):
		upper = strings.TrimSuffix(upper, "M")
		f64, err = strconv.ParseFloat(upper, 64)
		freq = uint32(f64 * 1e6)
	default:
		if last := len(upper) - 1; last >= 0 {
			upper = upper[:last]
		}
		f64, err = strconv.ParseFloat(upper, 64)
		freq = uint32(f64)
	}
	return
}

func rtlsdrCallback(buf []byte, ctx *rtl.UserCtx) {
	var i int

	if dongle.mute > 0 && dongle.mute < len(buf) {
		for i = 0; i < dongle.mute; i++ {
			buf[i] = 127
		}
		dongle.mute = 0
	}
	if dongle.preRotate {
		rotate90(buf)
	}
	buf16 := make([]int16, len(buf))
	for i := range buf {
		buf16[i] = int16(buf[i]) - 127
	}
	//fmt.Fprintf(os.Stderr, "2 buf %x %x %x %x, buf16 %x %x %x %x, buf len %d\n", buf[0], buf[1], buf[2], buf[3], uint16(buf16[0]), uint16(buf16[1]), uint16(buf16[2]), uint16(buf16[3]), len(buf))

	dongle.lpChan <- buf16
}

// ReadAsync blocks until CancelAsync
func dongleRoutine(wg *sync.WaitGroup) {
	defer wg.Done()
	//err := dongle.dev.ReadAsync(rtlsdrCallback, nil, 0, rtl.DefaultBufLength)
	err := dongle.dev.ReadAsync(rtlsdrCallback, nil, 0, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ReadAsync failed, err %s\n", err)
	}

	close(dongle.lpChan)

	fmt.Fprintf(os.Stderr, "Returning from dongleRoutine\n")
}

func demodRoutine(wg *sync.WaitGroup) {
	var ok bool

	defer wg.Done()

	for {
		demod.lowpassed, ok = <-dongle.lpChan

		if !ok {
			close(output.resultChan)
			close(controller.hopChan)
			fmt.Fprintf(os.Stderr, "Returning from demodRoutine\n")
			return
		}

		demod.fullDemod()

		if demod.squelchLevel > 0 && demod.squelchHits > demod.conseqSquelch {
			// hair trigger
			demod.squelchHits = demod.conseqSquelch + 1
			controller.hopChan <- true
			continue
		}
		result := make([]int16, len(demod.lowpassed))
		copy(result, demod.lowpassed)
		output.resultChan <- result
	}
}

func optimalSettings(freq int) {
	// giant ball of hacks
	// seems unable to do a single pass, 2:1
	var captureFreq, captureRate int
	demod.downsample = (minimumRate / demod.rateIn) + 1
	/*
		if demod.downsamplePasses > 0 {
			demod.downsamplePasses = int(math.Log2(float64(demod.downsample)) + 1)
			demod.downsample = 1 << uint(demod.downsamplePasses)
		}
	*/
	captureFreq = freq
	captureRate = demod.downsample * demod.rateIn
	if dongle.preRotate {
		captureFreq = freq + captureRate/4
	}

	captureFreq += controller.edge * demod.rateIn / 2
	demod.outputScale = (1 << 15) / (128 * demod.downsample)
	if demod.outputScale < 1 {
		demod.outputScale = 1
	}
	if reflect.ValueOf(demod.modeDemod).Pointer() == reflect.ValueOf(fmDemod).Pointer() {
		demod.outputScale = 1
	}
	dongle.freq = uint32(captureFreq)
	dongle.rate = uint32(captureRate)
}

func controllerRoutine(wg *sync.WaitGroup) {
	var err error

	defer wg.Done()

	s := controller

	if s.wbMode {
		for i := range s.freqs {
			s.freqs[i] += 16000
		}
	}

	// set up primary channel
	optimalSettings(int(s.freqs[0]))
	demod.squelchLevel = squelchToRms(demod.squelchLevel, dongle, demod)

	// Set the frequency
	err = dongle.dev.SetCenterFreq(int(dongle.freq))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting frequency %d\n", dongle.freq)
		return
	}

	fmt.Fprintf(os.Stderr, "Tuned to %d Hz\n", dongle.freq)
	fmt.Fprintf(os.Stderr, "Oversampling input by: %dx.\n", demod.downsample)
	fmt.Fprintf(os.Stderr, "Oversampling output by: %dx.\n", demod.postDownsample)
	fmt.Fprintf(os.Stderr, "Buffer size: %0.2fms\n", 1000*0.5*float32(actualBufLen)/float32(dongle.rate))

	// Set the sample rate
	err = dongle.dev.SetSampleRate(int(dongle.rate))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting sample rate %d\n", dongle.rate)
		return
	}
	fmt.Fprintf(os.Stderr, "Sampling at %d S/s.\n", dongle.rate)
	fmt.Fprintf(os.Stderr, "Output at %d Hz.\n", demod.rateIn/demod.postDownsample)

	for {
		_, ok := <-controller.hopChan
		if !ok {
			fmt.Fprintf(os.Stderr, "Returning from controllerRoutine\n")
			return
		}

		if len(s.freqs) <= 1 {
			continue
		}
		// hacky hopping
		s.freqNow = (s.freqNow + 1) % len(s.freqs)
		//fmt.Fprintf(os.Stderr, "controller, freqnow %d\n", s.freqs[s.freqNow])
		optimalSettings(int(s.freqs[s.freqNow]))
		err = dongle.dev.SetCenterFreq(int(dongle.freq))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting frequency %d\n", dongle.freq)
			return
		}
		dongle.mute = bufferDump
	}
}

func outputRoutine(wg *sync.WaitGroup) {
	var err error

	defer wg.Done()
	for {
		result, ok := <-output.resultChan
		if !ok {
			fmt.Fprintf(os.Stderr, "Returning from outputRoutine\n")
			return
		}

		err = binary.Write(output.file, binary.LittleEndian, result)
		if err != nil {
			fmt.Fprintf(os.Stderr, "output write error: %s\n", err)
		}
	}
}

func (d *demodState) fullDemod() {
	var i int
	doSquelch := false

	lowPass(d)

	// power squelch
	if d.squelchLevel > 0 {
		sr := rms(d.lowpassed, 1)
		if sr < d.squelchLevel {
			doSquelch = true
		}
	}

	if doSquelch {
		d.squelchHits++
		for i = 0; i < len(d.lowpassed); i++ {
			d.lowpassed[i] = 0
		}
	} else {
		d.squelchHits = 0
	}
	/*
		if d.squelchLevel > 0 && d.squelchHits > d.conseqSquelch {
			d.agc.gainNum = d.agc.gainDen
		}
	*/

	d.modeDemod(d)
	if d.deemph {
		deemphFilter(d)
	}
	if d.rateOut2 > 0 {
		lowPassReal(d)
	}
}

func (f *frequencies) String() string {
	return fmt.Sprintf("%d", *f)
}

func (f *frequencies) Set(val string) error {
	freqs, err := setFreqs(val)
	if err != nil {
		return err
	}

	*f = append(*f, freqs...)

	return nil
}

func main() {
	var err error

	flag.IntVar(&dongle.devIndex, "d", 0, "dongle device index")
	flag.Var(&controller.freqs, "f", "frequency or range of frequencies, and step e.g 92.9M:100.1M:25k")
	flag.IntVar(&demod.squelchLevel, "l", 0, "squelch level")
	rateStr := flag.String("s", "24k", "sample rate")
	flag.IntVar(&dongle.ppmError, "p", 0, "ppm error")
	flag.IntVar(&dongle.gain, "g", autoGain, "gain level (defaults to autogain)")
	demodMode := flag.String("M", "am", "demodulation mode [fm, am]")

	flag.Parse()

	if *rateStr != "" {
		var rateIn uint32
		rateIn, err = freqHz(*rateStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse sample rate %s\n", err)
			return
		}
		demod.rateIn = int(rateIn)
		demod.rateOut = int(rateIn)
	}

	switch *demodMode {
	case "fm":
		demod.modeDemod = fmDemod
	case "wbfm":
		controller.wbMode = true
		demod.modeDemod = fmDemod
		demod.rateIn = 170000
		demod.rateOut = 170000
		demod.rateOut2 = 32000
		output.rate = 32000
		demod.customAtan = 1
		//demod.post_downsample = 4;
		demod.deemph = true
		demod.squelchLevel = 0
	default:
		demod.modeDemod = amDemod
	}

	if len(controller.freqs) == 0 {
		fmt.Fprintln(os.Stderr, "Please specify a frequency.")
		return
	}

	if len(controller.freqs) >= frequenciesLimit {
		fmt.Fprintf(os.Stderr, "Too many channels, maximum %d.\n", frequenciesLimit)
		return
	}

	if len(controller.freqs) > 1 && demod.squelchLevel == 0 {
		fmt.Fprintln(os.Stderr, "Please specify a squelch level.  Required for scanning multiple frequencies.")
		return
	}

	// quadruple sample_rate to limit to Δθ to ±π/2
	demod.rateIn *= demod.postDownsample

	if output.rate == 0 {
		output.rate = demod.rateOut
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

	if demod.deemph {
		demod.deemphA = int(
			//round(1.0/(1.0-math.Exp(-1.0/(float64(demod.rateOut)*75e-6))), 0),
			round(1.0 / (1.0 - math.Exp(-1.0/(float64(demod.rateOut)*75e-6)))),
		)
		fmt.Fprintf(os.Stderr, "Deempha %d\n", demod.deemphA)
	}
	// Set the tuner gain
	if dongle.gain == autoGain {
		fmt.Fprintf(os.Stderr, "Setting auto gain\n")
		err = dongle.dev.SetTunerGainMode(false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error setting tuner auto-gain: %s\n", err)
			return
		}
	} else {
		dongle.gain *= 10
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
		fmt.Fprintf(os.Stderr, "Tuner error set to %i ppm.\n", dongle.ppmError)
	}

	if output.filename == "-" {
		output.file = os.Stdout
	} else {
		output.file, err = os.Create(output.filename)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		defer output.file.Close()
	}

	// Reset endpoint before we start reading from it (mandatory)
	err = dongle.dev.ResetBuffer()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	signalChan := make(chan os.Signal, 1)
	quit := make(exitChan)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		for _ = range signalChan {
			fmt.Fprintln(os.Stderr, "\nReceived an interrupt, stopping services...")
			close(quit)
			return
		}
	}()
	var wg sync.WaitGroup

	wg.Add(4)

	go controllerRoutine(&wg)
	go outputRoutine(&wg)
	go demodRoutine(&wg)
	go dongleRoutine(&wg)

	controller.hopChan <- true

	<-quit
	fmt.Fprintf(os.Stderr, "rtlsdr CancelAsync()\n")
	if err := dongle.dev.CancelAsync(); err != nil {
		fmt.Fprintf(os.Stderr, "Error canceling async %s\n", err)
	}

	fmt.Fprintf(os.Stderr, "Waiting for goroutines to finish...\n")
	wg.Wait()

	fmt.Fprintf(os.Stderr, "Exiting...\n")
}
