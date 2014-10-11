package main

import (
	//"log"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	// "unsafe"

	//rtl "github.com/jpoirier/gortlsdr"
)

const (
	DEFAULT_SAMPLE_RATE = 24000
	DEFAULT_BUF_LENGTH  = (1 * 16384)
	MAXIMUM_OVERSAMPLE  = 16
	MAXIMUM_BUF_LENGTH  = (MAXIMUM_OVERSAMPLE * DEFAULT_BUF_LENGTH)
	AUTO_GAIN           = -100
	BUFFER_DUMP         = 4096

	FREQUENCIES_LIMIT = 1000
)

type frequencies []uint32

type dongleState struct {
	exitFlag int
	//rtlsdrDevT    *dev
	devIndex       int
	freq           uint32
	rate           uint32
	gain           int
	buf16          [MAXIMUM_BUF_LENGTH]uint16
	bufLen         uint32
	ppmError       int
	offsetTuning   int
	directSampling int
	mute           int
	demodTarget    *demodState
}

type demodState struct {
	exitFlag           int
	lowpassed          [MAXIMUM_BUF_LENGTH]int16
	lpLen              int
	lpIHist            [10][6]int16
	lpQHist            [10][6]int16
	result             [MAXIMUM_BUF_LENGTH]int16
	droopIHist         [9]int16
	droopQHist         [9]int16
	resultLen          int
	rateIn             int
	rateOut            int
	rateOut2           int
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
	exitFlag  int
	file      os.File
	filename  string
	result    [MAXIMUM_BUF_LENGTH]int16
	resultLen int
	rate      int
}

type controllerState struct {
	exitFlag int
	freqs    frequencies
	freqLen  int
	freqNow  int
	edge     int
	wbMode   int
}

// multiple of these, eventually
var dongle *dongleState
var demod *demodState
var output *outputState
var controller *controllerState

func (s *dongleState) Init() {
	s.rate = DEFAULT_SAMPLE_RATE
	s.gain = AUTO_GAIN // tenths of a dB
	s.demodTarget = demod
}

func (s *demodState) Init() {
	s.rateIn = DEFAULT_SAMPLE_RATE
	s.rateOut = DEFAULT_SAMPLE_RATE
	s.conseqSquelch = 10
	s.squelchHits = 11
	s.postDownsample = 1 // once this works, default = 4
	s.rateOut2 = -1      // flag for disabled
	s.modeDemod = fmDemod
	s.outputTarget = output
}

func (s *controllerState) Init() {
	s.freqs[0] = 100000000
}

func (s *outputState) Init() {
	s.rate = DEFAULT_SAMPLE_RATE
}

func fmDemod(fm *demodState) {
	var i, pcm int
	lp := fm.lowpassed
	pcm = polarDiscriminant(lp[0], lp[1], int16(fm.preR), int16(fm.preJ))
	fm.result[0] = int16(pcm)
	for i = 2; i < (fm.lpLen - 1); i += 2 {
		pcm = polarDiscriminant(lp[i], lp[i+1], lp[i-2], lp[i-1])
		fm.result[i/2] = int16(pcm)
	}
	fm.preR = int(lp[fm.lpLen-2])
	fm.preJ = int(lp[fm.lpLen-1])
	fm.resultLen = fm.lpLen / 2
}

func polarDiscriminant(ar, aj, br, bj int16) int {
	var cr, cj int16
	var angle float64
	cr = ar*br - aj*bj
	cj = aj*br + ar*bj
	angle = math.Atan2(float64(cj), float64(cr))
	return int(angle / 3.14159 * (1 << 14))
}

func (f frequencies) String() string {
	return fmt.Sprintf("%d", f)
}

func (f frequencies) Set(val string) (err error) {

	var start, stop, step uint32

	bits := strings.Split(val, ":")

	switch len(bits) {
	case 1:
		i, err := strconv.Atoi(b)
		if err != nil {
			return
		}
		f = append(f, uint32(i))
		return
	case 2:
		start = bits[0]
		stop = bits[1]

	case 3:
		start = bits[0]
		stop = bits[1]
		step = strconv.Atoi(bits[2])

	default:
		err = fmt.Errorf("Frequency range could not be parsed")
		return
	}

	for _, b := range bits {

		i, err := strconv.Atoi(b)
		if err != nil {
			return
		}
		f = append(f, uint32(i))
	}
	return
}

	char *start, *stop, *step;
	int i;
	start = arg;
	stop = strchr(start, ':') + 1;
	stop[-1] = '\0';
	step = strchr(stop, ':') + 1;
	step[-1] = '\0';
	for(i=(int)atofs(start); i<=(int)atofs(stop); i+=(int)atofs(step))
	{
		s->freqs[s->freq_len] = (uint32_t)i;
		s->freq_len++;
		if (s->freq_len >= FREQUENCIES_LIMIT) {
			break;}
	}
	stop[-1] = ':';
	step[-1] = ':';
}

func main() {

	dongle.Init()
	demod.Init()
	output.Init()
	controller.Init()

	flag.IntVar(&dongle.devIndex, "d", 0, "dongle device index")
	flag.Var(&controller.freqs, "f", "frequency or range of frequencies e.g 92.9:100.1")

	flag.Parse()

	/*
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
		nRead, err := dev.ReadSync(buffer, rtl.DefaultBufLength)
		if err != nil {
			log.Printf("\tReadSync Failed - error: %s, nRead %d\n", err, nRead)
		} else {
			log.Printf("\tReadSync %d\n", nRead)
		}

		log.Printf("Exiting...\n")
	*/
}
